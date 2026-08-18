import UIKit
import Capacitor
import UserNotifications

@UIApplicationMain
class AppDelegate: UIResponder, UIApplicationDelegate {

    var window: UIWindow?

    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?) -> Bool {
        registerNotificationCategories()
        return true
    }

    /// Registers the notification categories that give Podiom's notifications their action
    /// buttons.
    ///
    /// On iOS the APNs category is the only thing that makes buttons appear. The Push Relay
    /// sets it from the `action_set` Podiom sends, so these identifiers and the action
    /// identifiers inside them have to match Podiom's notification registry exactly — a
    /// category iOS does not know about produces a notification with no buttons, and no
    /// error anywhere. `TestIOSRegistersEveryActionSet` in internal/notify pins both sides.
    ///
    /// iOS keeps registered categories across launches, so a notification arriving while
    /// the app is not running still finds them. Re-registering every launch is how they stay
    /// current after an update.
    ///
    /// Deliberately absent: the `question` category. Its buttons are the question's own
    /// answer options, whose text is only known when the question is asked, and a category's
    /// action titles are fixed at registration. Generic "Option 1" buttons would be worse
    /// than tapping through to read the actual question, so a question notification opens
    /// Podiom instead.
    private func registerNotificationCategories() {
        let center = UNUserNotificationCenter.current()

        // A tool call waiting on a decision. Allowing requires an unlocked device: it grants
        // an agent a capability, which is not something to hand over from a lock screen.
        let permission = UNNotificationCategory(
            identifier: "session_permission",
            actions: [
                UNNotificationAction(identifier: "deny", title: "Deny", options: [.destructive]),
                UNNotificationAction(identifier: "allow", title: "Allow", options: [.authenticationRequired])
            ],
            intentIdentifiers: [],
            options: []
        )

        // An agent asking for access to something. Same reasoning for the unlock requirement.
        let access = UNNotificationCategory(
            identifier: "access_request",
            actions: [
                UNNotificationAction(identifier: "deny", title: "Deny", options: [.destructive]),
                UNNotificationAction(identifier: "approve", title: "Approve", options: [.authenticationRequired])
            ],
            intentIdentifiers: [],
            options: []
        )

        // Work handed back to the user. Answering either way is theirs to give and reverses
        // nothing, so neither button needs an unlock.
        let actionItem = UNNotificationCategory(
            identifier: "goal_action_item",
            actions: [
                UNNotificationAction(identifier: "open", title: "Open", options: [.foreground]),
                UNNotificationAction(identifier: "blocked", title: "Can't do", options: []),
                UNNotificationAction(identifier: "done", title: "Done", options: [])
            ],
            intentIdentifiers: [],
            options: []
        )

        // An agent proposing a goal is finished. Review opens Podiom because the closing
        // report is the whole point of reviewing it.
        let completion = UNNotificationCategory(
            identifier: "goal_completion",
            actions: [
                UNNotificationAction(identifier: "review", title: "Review", options: [.foreground]),
                UNNotificationAction(identifier: "mark_done", title: "Mark done", options: [])
            ],
            intentIdentifiers: [],
            options: []
        )

        center.setNotificationCategories([permission, access, actionItem, completion])
    }

    func applicationWillResignActive(_ application: UIApplication) {
        // Sent when the application is about to move from active to inactive state. This can occur for certain types of temporary interruptions (such as an incoming phone call or SMS message) or when the user quits the application and it begins the transition to the background state.
        // Use this method to pause ongoing tasks, disable timers, and invalidate graphics rendering callbacks. Games should use this method to pause the game.
    }

    func applicationDidEnterBackground(_ application: UIApplication) {
        // Use this method to release shared resources, save user data, invalidate timers, and store enough application state information to restore your application to its current state in case it is terminated later.
        // If your application supports background execution, this method is called instead of applicationWillTerminate: when the user quits.
    }

    func applicationWillEnterForeground(_ application: UIApplication) {
        // Called as part of the transition from the background to the active state; here you can undo many of the changes made on entering the background.
    }

    func applicationDidBecomeActive(_ application: UIApplication) {
        // Restart any tasks that were paused (or not yet started) while the application was inactive. If the application was previously in the background, optionally refresh the user interface.
    }

    func applicationWillTerminate(_ application: UIApplication) {
        // Called when the application is about to terminate. Save data if appropriate. See also applicationDidEnterBackground:.
    }

    func application(_ application: UIApplication,
                     configurationForConnecting connectingSceneSession: UISceneSession,
                     options: UIScene.ConnectionOptions) -> UISceneConfiguration {
        let config = UISceneConfiguration(name: "Default Configuration",
                                          sessionRole: connectingSceneSession.role)
        config.delegateClass = SceneDelegate.self
        return config
    }
}
