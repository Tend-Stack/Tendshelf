package web

// Copy for the word-timing page (/admin/runtimes, M5 R3 P5). Nothing here is in the copy deck except where
// a line says so; every NEW line follows the voice rules in BRAND.md and is listed in the package report.
const (
	runtimesTitle = "Word timing"                                                                                                                                                                                                                                           // NEW
	runtimesNav   = "Word timing"                                                                                                                                                                                                                                           // NEW
	runtimesLead  = "Your server"                                                                                                                                                                                                                                           // same overline as the other server pages
	runtimesIntro = "Read-along lights up each sentence as it is spoken. For that, a speech model on this server listens to the audiobook and finds where every sentence starts. Tendshelf never downloads it: you install it once with the steps below, then press Check." // NEW

	runtimesEnglish = "English" // NEW
	runtimesSpanish = "Spanish" // NEW

	runtimesStateReady    = "Ready"         // NEW
	runtimesStateNotSetUp = "Not set up"    // NEW
	runtimesStateCheck    = "Needs a check" // NEW
	runtimesStateProblem  = "Problem"       // NEW

	runtimesLineReady    = "Word timing for %s is ready. Read-along can be prepared for %s books."                     // NEW
	runtimesLineNotSetUp = "Word timing is not installed."                                                             // NEW
	runtimesLineNoPython = "Word timing is not installed. Tendshelf found no Python at %s."                            // NEW
	runtimesLineUnsafe   = "Tendshelf will not run the Python at %s, because other accounts could change it."          // NEW
	runtimesLineNoModels = "Python is there, but the models folder %s is missing."                                     // NEW
	runtimesLineNeeds    = "Installed, but not tested yet. Press Check and Tendshelf will test it."                    // NEW
	runtimesLineModels   = "The model files for %s are not all in %s."                                                 // NEW
	runtimesLineMismatch = "A model file is missing or is not the pinned version. Fetch it again from the model page." // NEW
	runtimesLineIdentity = "The installed Python packages are not the pinned versions. Install them again."            // NEW
	runtimesLineDescribe = "Python started, but could not load the model. Check the install steps."                    // NEW
	runtimesLineOff      = "Word timing for this language is switched off."                                            // NEW
	runtimesLineOther    = "The check did not pass. Check the install steps and try again."                            // NEW

	runtimesCheck       = "Check"                                               // NEW
	runtimesCheckAria   = "Check word timing for %s"                            // NEW
	runtimesChecking    = "Checking %s. This can take a minute."                // NEW
	runtimesCheckFailed = "The check did not finish. Press Check to try again." // NEW
	runtimesCheckLink   = "Check the result"                                    // same words as the backup status link
	runtimesBadLanguage = "Choose English or Spanish."                          // NEW

	runtimesFactModel   = "Model"                                                                   // NEW
	runtimesFactRev     = "Revision"                                                                // NEW
	runtimesFactSize    = "Download"                                                                // NEW
	runtimesFactLicence = "Licence"                                                                 // NEW
	runtimesFactPage    = "Official model page"                                                     // NEW
	runtimesFactPython  = "Python"                                                                  // NEW
	runtimesFactThreads = "Threads"                                                                 // NEW
	runtimesFactTime    = "Time needed"                                                             // NEW
	runtimesTimeEn      = "About 2 to 4 minutes of computing for each hour of audio (an estimate)"  // NEW
	runtimesTimeEs      = "About 8 to 15 minutes of computing for each hour of audio (an estimate)" // NEW
	runtimesPageText    = "View on Hugging Face"                                                    // NEW

	runtimesStepsTitle = "How to install it"                                                                                                     // NEW
	runtimesStepsLead  = "Run these on the computer that runs Tendshelf. Tendshelf itself fetches nothing; the commands below are yours to run." // NEW
	runtimesStep1      = "Make the folders and a Python environment (version 3.12.14)."                                                          // NEW
	runtimesStep2      = "Save these two files, then install them. They pin every package by hash."                                              // NEW
	runtimesStep3      = "Fetch the model files from the official page, at the pinned revision. Tendshelf checks every file's hash."             // NEW
	runtimesStep4      = "Come back to this page and press Check for each language. The card must say Ready."                                    // NEW
	runtimesStepCode   = "Commands for step %d"                                                                                                  // NEW: aria label of a code block
	runtimesDownload   = "Download %s"                                                                                                           // NEW
)
