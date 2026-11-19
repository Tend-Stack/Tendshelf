package app.tendshelf.shared

/** Which phone app is asking: the word the server reads from `ts_shell` (`shell=` in a link). */
enum class AppShell(val word: String) {
    ANDROID("android"),
    IOS("ios"),
}
