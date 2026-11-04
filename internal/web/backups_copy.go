package web

// Scheduled backup copy. Every string here is NEW: none is in the copy deck yet. They follow the voice
// rules in BRAND.md.
const (
	backupsTitle    = "Backups"
	backupsIntro    = "Tendshelf keeps dated copies of its database: your people, progress, bookmarks and settings. Your audiobook files are not copied."
	backupsHeading  = "Automatic backups"
	backupsOff      = "Off"
	backupsDaily    = "Every day"
	backupsWeekly   = "Every week"
	backupsSave     = "Save"
	backupsSaved    = "Backup schedule saved."
	backupsLast     = "Last backup %s · %s"
	backupsNone     = "No backup yet"
	backupsNext     = "Next backup %s"
	backupsDir      = "Copies kept here: %s"
	backupsNow      = "Back up now"
	backupsRunning  = "Backing up…"
	backupsDone     = "Backed up %s"
	backupsKeeps    = "Tendshelf keeps the newest 7 copies. Copies made before an update are kept separately."
	backupsFailed   = "The backup didn't finish. Check that the data folder has free space."
	backupsErrValue = "Pick off, every day or every week."
	backupsCopies   = "Copies"

	backupsRestoreTitle = "How to restore a copy"
	backupsRestore1     = "Stop Tendshelf."
	backupsRestore2     = "Replace tendshelf.db in the data folder with the copy, and delete tendshelf.db-wal and tendshelf.db-shm if they are there."
	backupsRestore3     = "Start Tendshelf again."
)
