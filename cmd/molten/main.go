package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/pixelate-it/molten/internal/version"
)

var errUsage = errors.New("usage error")

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, errUsage) {
			printUsage()
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errUsage
	}

	switch args[0] {
	case "-v", "--version", "version":
		for _, line := range version.Statement() {
			fmt.Println(line)
		}
		return nil

	case "info":
		return runInfo(args[1:])

	case "render":
		return runRender(args[1:])

	case "-h", "--help", "help":
		printUsage()
		return nil

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", args[0])
		return errUsage
	}
}

func printUsage() {
	fmt.Println(`Molten - PixelBattle canvas recording format & renderer.

Usage:
  molten <command> [flags]

Commands:
  info     Inspect a .mltn recording file
  render   Render a .mltn recording into a video file
  version  Print version information

Flags:
  -v, --version   Print version information
  -h, --help      Show this help message

Run 'molten <command> -h' for command-specific flags.`)
}
