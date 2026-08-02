package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"eve-beemo/internal/lifecycle"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "beemo:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return flag.ErrHelp
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	switch args[0] {
	case "up":
		flags, common := commandFlags("up")
		build := flags.Bool("build", true, "build changed service images")
		memory := flags.Bool("memory", true, "start Memory Palace")
		ui := flags.Bool("ui", true, "start the Beemo HTTP UI")
		voice := flags.Bool("voice", false, "start ASR and wake-word services")
		code := flags.Bool("code", true, "start the local Beemo code tools")
		timeout := flags.Duration("timeout", 5*time.Minute, "readiness timeout per service")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		manager, err := newManager(common)
		if err != nil {
			return err
		}
		return manager.Up(ctx, lifecycle.UpOptions{Build: *build, Memory: *memory, UI: *ui, Voice: *voice, Code: *code, Timeout: *timeout})
	case "chat":
		binary := ""
		if executable, err := os.Executable(); err == nil {
			candidate := filepath.Join(filepath.Dir(executable), "beemo-chat")
			if _, statErr := os.Stat(candidate); statErr == nil {
				binary = candidate
			}
		}
		if binary == "" {
			paths, err := lifecycle.ResolvePaths("", "")
			if err != nil {
				return err
			}
			binary = filepath.Join(paths.BeemoRoot, "bin", "beemo-chat")
		}
		command := exec.CommandContext(ctx, binary, args[1:]...)
		command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
		return command.Run()
	case "init":
		flags, common := commandFlags("init")
		accelerator := flags.String("accelerator", "gpu", "model runtime accelerator: gpu or cpu")
		models := flags.Bool("models", true, "download configured models")
		force := flags.Bool("force-download", false, "replace existing model downloads")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		manager, err := newManager(common)
		if err != nil {
			return err
		}
		return manager.Init(ctx, *accelerator, *models, *force)
	case "down":
		flags, common := commandFlags("down")
		memory := flags.Bool("memory", true, "stop Memory Palace")
		ui := flags.Bool("ui", true, "stop the Beemo HTTP UI")
		voice := flags.Bool("voice", true, "stop ASR and wake-word services")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		manager, err := newManager(common)
		if err != nil {
			return err
		}
		return manager.Down(ctx, *memory, *ui, *voice)
	case "status":
		flags, common := commandFlags("status")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		manager, err := newManager(common)
		if err != nil {
			return err
		}
		return manager.Status(ctx)
	case "doctor":
		flags, common := commandFlags("doctor")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		manager, err := newManager(common)
		if err != nil {
			return err
		}
		return manager.Doctor(ctx)
	case "logs":
		flags, common := commandFlags("logs")
		tail := flags.Int("tail", 200, "number of existing lines")
		follow := flags.Bool("follow", true, "follow new output")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		service := "eve-orchestrator"
		if flags.NArg() > 0 {
			service = flags.Arg(0)
		}
		manager, err := newManager(common)
		if err != nil {
			return err
		}
		return manager.Logs(ctx, service, *tail, *follow)
	case "restart":
		flags, common := commandFlags("restart")
		build := flags.Bool("build", true, "build changed service images")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 1 {
			return fmt.Errorf("restart requires exactly one service")
		}
		manager, err := newManager(common)
		if err != nil {
			return err
		}
		return manager.Restart(ctx, flags.Arg(0), *build)
	case "version":
		fmt.Println("beemo dev")
		return nil
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

type commonOptions struct {
	profile    *string
	beemoRoot  *string
	memoryRoot *string
}

func commandFlags(name string) (*flag.FlagSet, commonOptions) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	common := commonOptions{
		profile:    flags.String("profile", envOrDefault("BEEMO_PROFILE", lifecycle.DefaultProfile), "machine/runtime profile"),
		beemoRoot:  flags.String("root", "", "Beemo project root"),
		memoryRoot: flags.String("memory-root", "", "Memory Palace project root"),
	}
	return flags, common
}

func newManager(options commonOptions) (lifecycle.Manager, error) {
	paths, err := lifecycle.ResolvePaths(*options.beemoRoot, *options.memoryRoot)
	if err != nil {
		return lifecycle.Manager{}, err
	}
	profile, err := lifecycle.ResolveProfile(*options.profile)
	if err != nil {
		return lifecycle.Manager{}, err
	}
	return lifecycle.Manager{
		Paths:   paths,
		Profile: profile,
		Runner:  lifecycle.ExecRunner{Stdout: os.Stdout, Stderr: os.Stderr},
		Output:  os.Stdout,
	}, nil
}

func usage() {
	fmt.Print(`usage: beemo <command> [options]

Commands:
  up       start models, memory, orchestrator, UI, and optional voice
  init     download configured models for first use
  down     stop Beemo services without stopping PostgreSQL
  restart  rebuild and recreate one service
  status   show container and readiness state
  doctor   validate Docker and the reused Compose manifests
  logs     follow service logs
  chat     open the interactive Beemo conversation client
  version  print version information

Profiles: garnetmoon (default), legion-go, vllm-gpu, vllm-cpu, llama-cpu

Examples:
  beemo up --profile garnetmoon
  beemo up --profile legion-go --ui=false
  beemo status
  beemo chat --mode code --workspace /path/to/repository
  beemo logs --tail 100 memory_palace
`)
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
