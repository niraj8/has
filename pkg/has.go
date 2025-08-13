package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
)

const HAS_VERSION = "v1.5.1"
const FANCY_X = '✗'
const CHECKMARK = '✓'

var OK = 0
var KO = 0

// var Reset = "\033[0m"
// var Red = "\033[31m"
// var Green = "\033[32m"

// FIXME the go version doesn't maintain a mapping of which flag/arg to check for which tool
//       this is problematic as we are running the tool and it could make changes that aren't desirable instead of listing version
//       basically this always runs in UNSAFE mode

var REGEX_SIMPLE_VERSION = regexp.MustCompile(`(?m)(\d+\.?){2,3}`)

type Detector interface {
	Name() string
	Detect(ctx context.Context, tool string) (*Result, error)
}

type Result struct {
	Command string
	Found   bool
	Version string
	// IDEA: in verbose mode we can print "✓ git 2.15.0 with `git --version`"
	Source string // which detector found it
}

type VersionFlagDetector struct {
	flag string
}

func (v *VersionFlagDetector) Name() string {
	return fmt.Sprintf("VersionFlagDetector:%s", v.flag)
}

func (v *VersionFlagDetector) Detect(ctx context.Context, tool string) (*Result, error) {
	// check exit code is not non-zero
	// check exit code is not 127, command not found
	// capture output and parse version
	cmd := exec.CommandContext(ctx, tool, v.flag)
	version, err := detectVersion(cmd)
	if err != nil {
		slog.Debug("failed to detect version for %s: %v", tool, err)
		return nil, err
	}
	return &Result{
		Command: tool,
		Found:   true,
		Version: *version,
		Source:  v.Name(),
	}, nil
}

func detectVersion(cmd *exec.Cmd) (*string, error) {
	buf := new(bytes.Buffer)
	cmd.Stdout = buf
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			slog.Debug("cmd exited unsuccessfully", slog.Int("exit code", exiterr.ExitCode()))
		} else {
			slog.Debug("cmd exited with error", slog.String("error", err.Error()))
		}
		return nil, err
	}

	var version string
	for _, match := range REGEX_SIMPLE_VERSION.FindAllString(buf.String(), -1) {
		version = match
		break
	}
	return &version, nil
}

type VersionArgDetector struct {
	arg string
}

func (v *VersionArgDetector) Name() string {
	return fmt.Sprintf("VersionArgDetector:%s", v.arg)
}

func (v *VersionArgDetector) Detect(ctx context.Context, tool string) (*Result, error) {
	cmd := exec.CommandContext(ctx, tool, v.arg)
	version, err := detectVersion(cmd)
	if err != nil {
		slog.Debug("failed to detect version for %s: %v", tool, err)
		return nil, err
	}
	return &Result{
		Command: tool,
		Found:   true,
		Version: *version,
		Source:  v.Name(),
	}, nil
}

func main() {
	ctx := context.Background()
	detectors := []Detector{
		&VersionFlagDetector{"--version"},
		&VersionFlagDetector{"-V"},
		&VersionFlagDetector{"-v"},
		&VersionArgDetector{"version"},
	}

	quiet := flag.Bool("q", false, "Silent mode")
	helpFlag := flag.Bool("h", false, "Display this help text and quit")
	versionFlag := flag.Bool("v", false, "Show version number and quit")

	flag.Parse()
	argsTools := flag.Args()

	if *helpFlag {
		flag.Usage()
		os.Exit(0)
	}

	if *versionFlag {
		fmt.Println(HAS_VERSION)
		os.Exit(0)
	}

	// .hasrc
	file, err := os.Open(".hasrc")
	if err != nil {
		slog.Debug("error reading .hasrc", slog.String("error", err.Error()))
	}
	scanner := bufio.NewScanner(file)
	tools := make([]string, 0)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "#") && !slices.Contains(argsTools, line) {
			tools = append(tools, line)
		}
	}
	tools = slices.Concat(argsTools, tools)

	if len(tools) == 0 {
		flag.Usage()
		os.Exit(0)
	}

	if err := scanner.Err(); err != nil {
		slog.Debug("error reading .hasrc", slog.String("error", err.Error()))
	}

	// FIXME maintain order while printing results
	results := make(map[string]*Result)
	// IDEA try errgroups instead
	var wg sync.WaitGroup

	for _, tool := range tools {
		wg.Add(1)
		go func(t string) {
			defer wg.Done()
			results[t] = detectTool(ctx, detectors, t, *quiet)
		}(tool)
	}
	wg.Wait()
	slog.Debug("exiting: ", slog.Int("KO", KO))
	if KO > 0 {
		if KO > 126 {
			os.Exit(126)
		} else {
			os.Exit(KO)
		}
	}
}

func detectTool(ctx context.Context, detectors []Detector, tool string, quiet bool) *Result {
	for _, detector := range detectors {
		if result, err := detector.Detect(ctx, tool); err != nil {
			slog.Debug(err.Error())
		} else {
			if result.Found {
				if !quiet {
					fmt.Printf("%c %s %s\n", CHECKMARK, result.Command, result.Version)
				}
				OK += 1
				return result
			}
		}
	}
	if !quiet {
		fmt.Printf("%c %s not understood\n", FANCY_X, tool)
	}
	KO += 1
	return nil
}
