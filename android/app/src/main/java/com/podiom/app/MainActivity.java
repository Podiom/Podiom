package com.podiom.app;

import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.os.Build;
import android.os.Bundle;

import com.getcapacitor.BridgeActivity;

public class MainActivity extends BridgeActivity {

    /**
     * Notification channels, one per Podiom importance level.
     *
     * Podiom decides how much a notification is worth interrupting for, and Android
     * expresses that through channels — so the mapping lives here and the payload names
     * the channel it belongs to. Without this, everything would arrive at one importance
     * and a routine progress update would buzz exactly like an agent waiting on approval.
     *
     * The ids are part of the delivery contract. The relay derives the channel from the
     * notification's importance and names it in the FCM payload, so these have to match
     * the ids it sends exactly — a channel that does not exist here means the
     * notification is filed under Android's default and the user's per-importance
     * settings stop applying, silently.
     */
    private static final String CHANNEL_PASSIVE = "podiom_passive";
    private static final String CHANNEL_DEFAULT = "podiom_default";
    private static final String CHANNEL_IMPORTANT = "podiom_important";
    private static final String CHANNEL_CRITICAL = "podiom_critical";

    @Override
    public void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        createNotificationChannels();
    }

    /**
     * Channels must exist before the first notification that names one arrives, and
     * creating one that already exists is a no-op, so this runs on every launch.
     *
     * A user who turns a channel down keeps that choice: Android ignores importance
     * changes to an existing channel, which is deliberate on its part and correct here —
     * Podiom should not be able to override someone silencing its progress updates.
     */
    private void createNotificationChannels() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) {
            return;
        }
        NotificationManager manager = getSystemService(NotificationManager.class);
        if (manager == null) {
            return;
        }
        manager.createNotificationChannel(new NotificationChannel(
                CHANNEL_PASSIVE, "Activity", NotificationManager.IMPORTANCE_MIN));
        manager.createNotificationChannel(new NotificationChannel(
                CHANNEL_DEFAULT, "Updates", NotificationManager.IMPORTANCE_DEFAULT));
        manager.createNotificationChannel(new NotificationChannel(
                CHANNEL_IMPORTANT, "Needs you", NotificationManager.IMPORTANCE_HIGH));
        manager.createNotificationChannel(new NotificationChannel(
                CHANNEL_CRITICAL, "Failures", NotificationManager.IMPORTANCE_HIGH));
    }
}
