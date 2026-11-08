package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/fishandsheep/jkv/internal/catalog"
	"github.com/fishandsheep/jkv/internal/selfmanage"
	"github.com/fishandsheep/jkv/internal/store"
)

var version = "dev"

// Release workflow injects catalog trust values into release binaries. Self
// endpoint values are test seams; release binaries use the fixed HTTPS defaults.

var (
	catalogKeyID              = ""
	catalogPublicKeyBase64    = ""
	catalogCNBDownloadBase    = "https://cnb.cool/fishandsheep/jkv-catalog/-/releases/download"
	catalogGitHubDownloadBase = "https://github.com/fishandsheep/jkv-catalog/releases/download"
	catalogHTTPClient         = http.DefaultClient
	selfCNBLatestURL          = ""
	selfGitHubLatestURL       = ""
	selfCNBDownloadBase       = ""
	selfGitHubDownloadBase    = ""
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(exitCodeFor(err))
	}
}

const (
	exitUsage     = 2
	exitNotFound  = 3
	exitNetwork   = 4
	exitIntegrity = 5
	exitState     = 6
)

func exitCodeFor(err error) int {
	var cliErr *cliError
	if errors.As(err, &cliErr) {
		return cliErr.code
	}
	switch {
	case errors.Is(err, store.ErrIntegrity), errors.Is(err, selfmanage.ErrIntegrity):
		return exitIntegrity
	case errors.Is(err, store.ErrNetwork), errors.Is(err, selfmanage.ErrNetwork):
		return exitNetwork
	case errors.Is(err, store.ErrState), errors.Is(err, selfmanage.ErrState):
		return exitState
	case errors.Is(err, store.ErrNotFound):
		return exitNotFound
	}
	var download *catalog.DownloadError
	if errors.As(err, &download) {
		return exitNetwork
	}
	return 1
}

func run(ctx context.Context, args []string) error {
	options, args, err := parseGlobalOptions(args)
	if err != nil {
		return err
	}
	ctx = context.WithValue(ctx, cliOptionsKey{}, options)
	if len(args) == 0 {
		usage()
		return nil
	}
	if command, ok := commandAliases[args[0]]; ok && command != "help" {
		for _, arg := range args[1:] {
			if arg == "-h" || arg == "--help" {
				printCommandHelp(command)
				return nil
			}
		}
	}
	s := store.New(store.DefaultRoot())
	switch args[0] {
	case "list", "ls":
		return cmdList(ctx, s, args[1:])
	case "install", "i":
		return cmdInstall(ctx, s, args[1:])
	case "repair":
		return cmdRepair(ctx, s, args[1:])
	case "use", "u":
		return cmdUse(s, args[1:])
	case "default", "d":
		return cmdDefault(s, args[1:])
	case "current", "c":
		return cmdCurrent(ctx, s, args[1:])
	case "uninstall", "rm":
		return cmdUninstall(s, args[1:])
	case "self", "s":
		return cmdSelf(ctx, s, args[1:])
	case "home", "h":
		return cmdHome(ctx, s, args[1:])
	case "env", "e":
		return cmdEnv(s, args[1:])
	case "init", "in":
		return cmdInit(args[1:])
	case "mirror", "m":
		return cmdMirror(ctx, args[1:])
	case "clean", "cl":
		return cmdClean(ctx, s, args[1:])
	case "doctor":
		return cmdDoctor(ctx, s, args[1:])
	case "version", "v", "--version", "-v":
		if options.JSON {
			return writeJSON(map[string]string{"name": "jkv", "version": version})
		}
		fmt.Println("jkv", version)
		return nil
	case "help", "-h", "--help":
		usage()
		return nil
	case "__complete":
		return cmdComplete(ctx, s, args[1:])
	default:
		return usageErrorf("未知命令 %q；运行 jkv help", args[0])
	}
}

type cliOptionsKey struct{}

type cliOptions struct {
	JSON    bool
	Quiet   bool
	Verbose bool
}

func parseGlobalOptions(args []string) (cliOptions, []string, error) {
	var options cliOptions
	out := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "--json":
			options.JSON = true
		case "--quiet":
			options.Quiet = true
		case "--verbose":
			options.Verbose = true
		default:
			out = append(out, arg)
		}
	}
	return options, out, nil
}

func optionsFromContext(ctx context.Context) cliOptions {
	options, _ := ctx.Value(cliOptionsKey{}).(cliOptions)
	return options
}
