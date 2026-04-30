package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/upgrade"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/version"
	"github.com/spf13/pflag"
)

// Upgrade replaces the running ezs (and sibling ezs-mcp) binary with the
// matching artifact from the latest GitHub release. `go install` users
// are upgraded by re-running `go install` against the resolved tag;
// Homebrew users are routed back to `brew upgrade ezstack`.
func Upgrade(args []string) error {
	fs := pflag.NewFlagSet("upgrade", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sUpgrade ezs and ezs-mcp to the latest release%s

%sUSAGE%s
    ezs upgrade [options]
    ezs update  [options]

%sDESCRIPTION%s
    Downloads the matching release tarball from GitHub, verifies the
    SHA-256 against checksums.txt, and atomically replaces the running
    ezs binary on disk. If an ezs-mcp binary lives in the same directory
    (or on PATH), it is upgraded too.

    'go install' installations are upgraded by re-invoking 'go install'
    against the resolved tag (so the toolchain stays the source of truth
    for the install location). Homebrew installations are routed back to
    'brew upgrade ezstack' so brew's receipt of the install stays in
    sync with the binary on disk.

%sOPTIONS%s
    --check              Print current vs latest version and exit
    --version <tag>      Pin to a specific release tag (e.g. v4.6.3)
    --force              Reinstall even when already at the target
    --no-mcp             Skip the sibling ezs-mcp binary
    -y, --yes            Skip the replace-binaries confirmation
    -h, --help           Show this help message

%sEXIT CODES%s
    0   upgrade completed (or already at latest in --check mode)
    1   general error (download / checksum / I/O)
    2   usage error
    8   network error talking to api.github.com
   10   user declined the confirmation
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}

	checkFlag := fs.Bool("check", false, "Print current vs latest version and exit")
	versionFlag := fs.String("version", "", "Pin upgrade to a specific release tag")
	forceFlag := fs.Bool("force", false, "Reinstall even when already at the target")
	noMCP := fs.Bool("no-mcp", false, "Skip the sibling ezs-mcp binary")
	yesFlag := fs.BoolP("yes", "y", false, "Skip the replace-binaries confirmation")
	helpFlag := fs.BoolP("help", "h", false, "Show help")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return nil
		}
		return ui.NewExitError(ui.ExitUsage, "%v", err)
	}
	if *helpFlag {
		fs.Usage()
		return nil
	}

	confirm := func(prompt string) bool { return ui.ConfirmTUIWithDefault(prompt, true) }
	if *yesFlag || ui.YesMode {
		confirm = nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	res, err := upgrade.Run(ctx, upgrade.Options{
		CurrentVersion: version.Version,
		TargetTag:      *versionFlag,
		Force:          *forceFlag,
		CheckOnly:      *checkFlag,
		IncludeMCP:     !*noMCP,
		Confirm:        confirm,
		Logf:           func(format string, args ...any) { ui.Info(fmt.Sprintf(format, args...)) },
	})
	if err != nil {
		var managed *upgrade.ManagedInstallError
		if errors.As(err, &managed) {
			ui.Warn(managed.Error())
			return nil
		}
		var net *upgrade.NetworkError
		if errors.As(err, &net) {
			return ui.NewExitError(ui.ExitNetworkError, "%v", err)
		}
		return ui.NewExitError(ui.ExitGeneral, "%v", err)
	}

	switch {
	case res.Cancelled:
		return ui.NewExitError(ui.ExitUserCancelled, "upgrade cancelled")
	case res.AlreadyAtTip:
		return nil
	case *checkFlag:
		return nil
	}

	ui.Success(fmt.Sprintf("ezstack upgraded %s → %s", res.From, res.To))
	if len(res.Updated) > 1 {
		ui.Info(fmt.Sprintf("replaced %d binaries", len(res.Updated)))
	}
	return nil
}
