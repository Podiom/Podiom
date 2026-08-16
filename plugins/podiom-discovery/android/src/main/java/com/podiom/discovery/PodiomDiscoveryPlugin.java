package com.podiom.discovery;

import android.content.Context;
import android.net.nsd.NsdManager;
import android.net.nsd.NsdServiceInfo;
import android.net.wifi.WifiManager;
import android.os.Handler;
import android.os.Looper;

import com.getcapacitor.JSArray;
import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;

import java.net.InetAddress;
import java.util.ArrayDeque;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Queue;

/**
 * Browses the local network for Podiom daemons (R8).
 *
 * <p>mDNS needs multicast sockets a WebView cannot open, so the browse runs here
 * and returns a plain list. It only ever reads: nothing is registered, and no
 * connection is made to anything found.
 */
@CapacitorPlugin(name = "PodiomDiscovery")
public class PodiomDiscoveryPlugin extends Plugin {

    private static final String SERVICE_TYPE = "_podiom._tcp.";
    private static final int DEFAULT_TIMEOUT_MS = 4000;

    @PluginMethod
    public void discover(PluginCall call) {
        Integer timeout = call.getInt("timeoutMs");
        new BrowseSession(getContext(), call).start(timeout == null ? DEFAULT_TIMEOUT_MS : timeout);
    }

    /**
     * One browse, from start to timeout. mDNS has no "complete" signal — hosts
     * simply stop answering — so the session runs for a fixed window and reports
     * whatever resolved within it.
     */
    private static final class BrowseSession {
        private final Context context;
        private final PluginCall call;
        private final Handler handler = new Handler(Looper.getMainLooper());
        // Keyed by address so one daemon seen on several interfaces is listed once.
        private final Map<String, JSObject> found = new LinkedHashMap<>();
        // NsdManager serialises resolution: a second resolveService while one is
        // in flight fails with FAILURE_ALREADY_ACTIVE, so they are queued.
        private final Queue<NsdServiceInfo> pending = new ArrayDeque<>();

        private NsdManager nsd;
        private NsdManager.DiscoveryListener listener;
        private WifiManager.MulticastLock lock;
        private boolean resolving;
        private boolean finished;

        BrowseSession(Context context, PluginCall call) {
            this.context = context;
            this.call = call;
        }

        void start(int timeoutMs) {
            nsd = (NsdManager) context.getSystemService(Context.NSD_SERVICE);
            if (nsd == null) {
                finish();
                return;
            }

            acquireMulticastLock();

            listener = new NsdManager.DiscoveryListener() {
                @Override
                public void onDiscoveryStarted(String serviceType) {}

                @Override
                public void onServiceFound(NsdServiceInfo info) {
                    synchronized (BrowseSession.this) {
                        pending.add(info);
                    }
                    drain();
                }

                @Override
                public void onServiceLost(NsdServiceInfo info) {}

                @Override
                public void onDiscoveryStopped(String serviceType) {}

                @Override
                public void onStartDiscoveryFailed(String serviceType, int errorCode) {
                    // Nothing can be found — report an empty list rather than an
                    // error, so the connection screen keeps offering manual entry.
                    finish();
                }

                @Override
                public void onStopDiscoveryFailed(String serviceType, int errorCode) {}
            };

            try {
                nsd.discoverServices(SERVICE_TYPE, NsdManager.PROTOCOL_DNS_SD, listener);
            } catch (IllegalArgumentException e) {
                finish();
                return;
            }

            handler.postDelayed(this::finish, timeoutMs);
        }

        /** Resolves queued services one at a time, as NsdManager requires. */
        private void drain() {
            NsdServiceInfo next;
            synchronized (this) {
                if (resolving || finished) return;
                next = pending.poll();
                if (next == null) return;
                resolving = true;
            }

            nsd.resolveService(next, new NsdManager.ResolveListener() {
                @Override
                public void onResolveFailed(NsdServiceInfo info, int errorCode) {
                    synchronized (BrowseSession.this) {
                        resolving = false;
                    }
                    drain();
                }

                @Override
                public void onServiceResolved(NsdServiceInfo info) {
                    record(info);
                    synchronized (BrowseSession.this) {
                        resolving = false;
                    }
                    drain();
                }
            });
        }

        private void record(NsdServiceInfo info) {
            InetAddress address = info.getHost();
            if (address == null) return;
            // Strip the zone suffix IPv6 link-local addresses carry
            // ("fe80::1%wlan0"); a URL cannot hold one.
            String host = address.getHostAddress();
            if (host == null) return;
            int zone = host.indexOf('%');
            if (zone > 0) host = host.substring(0, zone);

            JSObject instance = new JSObject();
            instance.put("name", info.getServiceName());
            instance.put("host", host);
            instance.put("port", info.getPort());

            Map<String, byte[]> attrs = info.getAttributes();
            if (attrs != null && attrs.get("version") != null) {
                instance.put("version", new String(attrs.get("version")));
            }

            synchronized (this) {
                found.put(host + ":" + info.getPort(), instance);
            }
        }

        private void acquireMulticastLock() {
            try {
                WifiManager wifi = (WifiManager) context.getApplicationContext().getSystemService(Context.WIFI_SERVICE);
                if (wifi == null) return;
                lock = wifi.createMulticastLock("podiom-discovery");
                lock.setReferenceCounted(true);
                lock.acquire();
            } catch (SecurityException e) {
                // Without the lock the browse still runs; it just finds less on
                // devices that filter multicast.
                lock = null;
            }
        }

        private void finish() {
            JSArray instances = new JSArray();
            synchronized (this) {
                if (finished) return;
                finished = true;
                for (JSObject instance : found.values()) {
                    instances.put(instance);
                }
            }

            handler.removeCallbacksAndMessages(null);
            if (nsd != null && listener != null) {
                try {
                    nsd.stopServiceDiscovery(listener);
                } catch (IllegalArgumentException e) {
                    // Discovery was never successfully started.
                }
            }
            if (lock != null && lock.isHeld()) {
                lock.release();
            }

            JSObject result = new JSObject();
            result.put("instances", instances);
            call.resolve(result);
        }
    }
}
