package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tokenlive/tokenlive-gateway/pkg/config"
)

// Info holds build-time and runtime default paths.
type Info struct {
	Version             string
	DefaultConfigPath   string
	DefaultDataDir      string
	DefaultAdminWorkDir string
	DefaultAdminStatic  string
}

// LogFileInfo describes a discovered log file.
type LogFileInfo struct {
	Tag     string // "service-err", "service-out", "app-log"
	Path    string
	Size    int64
	ModTime time.Time
	Exists  bool
}

var errorKeywords = []string{
	"ERROR", "FATAL", "PANIC", "panic:", "level=error", "level=fatal",
	`"level":"error"`, `"level":"fatal"`, "[ERROR]", "[FATAL]", "err=",
}

// DiscoverLogs finds available log files based on runtime flags and build defaults.
func DiscoverLogs(info Info, confPath, dataDir string) []LogFileInfo {
	var list []LogFileInfo
	seen := make(map[string]bool)

	add := func(tag, path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		item := LogFileInfo{Tag: tag, Path: path}
		if st, err := os.Stat(path); err == nil {
			item.Exists = true
			item.Size = st.Size()
			item.ModTime = st.ModTime()
		}
		list = append(list, item)
	}

	// 1. Homebrew prefix detection
	brewPrefix := detectBrewPrefix(info.DefaultConfigPath)

	if brewPrefix != "" {
		add("service-err", filepath.Join(brewPrefix, "var", "log", "tokenlive.err.log"))
		add("service-out", filepath.Join(brewPrefix, "var", "log", "tokenlive.log"))
	}

	// 2. Data directory log file
	resolvedData := dataDir
	if resolvedData == "" {
		resolvedData = firstNonEmpty(info.DefaultDataDir, "data")
	}
	add("app-log", filepath.Join(resolvedData, "logs", "tokenlive.log"))

	// 3. Check config log.log_file_name if available
	resolvedConf := confPath
	if resolvedConf == "" {
		resolvedConf = info.DefaultConfigPath
	}
	if resolvedConf != "" && fileExists(resolvedConf) {
		v := config.NewConfig(resolvedConf)
		if logFile := v.GetString("log.log_file_name"); logFile != "" {
			if !filepath.IsAbs(logFile) {
				logFile = filepath.Join(resolvedData, logFile)
			}
			add("config-log", logFile)
		}
	}

	return list
}

func detectBrewPrefix(defaultConfPath string) string {
	if defaultConfPath != "" && strings.HasSuffix(defaultConfPath, "/etc/tokenlive/config.yml") {
		return strings.TrimSuffix(defaultConfPath, "/etc/tokenlive/config.yml")
	}
	if env := os.Getenv("HOMEBREW_PREFIX"); env != "" {
		return env
	}
	if out, err := exec.Command("brew", "--prefix").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	for _, candidate := range []string{"/opt/homebrew", "/usr/local"} {
		if fileExists(filepath.Join(candidate, "var", "log")) {
			return candidate
		}
	}
	return ""
}

// RunLogs handles `tokenlive logs [flags]` subcommand.
func RunLogs(info Info, args []string) int {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	follow := fs.Bool("f", false, "follow log output (tail -f)")
	fs.BoolVar(follow, "follow", false, "follow log output (tail -f)")

	lines := fs.Int("n", 50, "number of lines to show")
	fs.IntVar(lines, "lines", 50, "number of lines to show")

	showErrOnly := fs.Bool("e", false, "show error logs only")
	fs.BoolVar(showErrOnly, "err", false, "show error logs only")
	fs.BoolVar(showErrOnly, "error", false, "show error logs only")

	appOnly := fs.Bool("app", false, "show app log only")
	serviceOnly := fs.Bool("service", false, "show service logs only")
	checkMode := fs.Bool("check", false, "check logs for errors and summarize")
	listMode := fs.Bool("list", false, "list discovered log files")

	confPath := fs.String("conf", "", "gateway config path")
	dataDir := fs.String("data-dir", "", "data directory")

	_ = fs.Parse(args)

	logs := DiscoverLogs(info, *confPath, *dataDir)

	if *listMode {
		fmt.Println("Discovered Log Files:")
		for _, l := range logs {
			status := "NOT FOUND"
			if l.Exists {
				status = fmt.Sprintf("EXISTS (%s, updated %s)", formatBytes(l.Size), l.ModTime.Format("15:04:05"))
			}
			fmt.Printf("  [%-11s] %s -> %s\n", l.Tag, l.Path, status)
		}
		return 0
	}

	if *checkMode {
		return checkErrors(logs)
	}

	// Filter target log files
	var targets []LogFileInfo
	for _, l := range logs {
		if !l.Exists {
			continue
		}
		if *appOnly && !strings.Contains(l.Tag, "app") && !strings.Contains(l.Tag, "config") {
			continue
		}
		if *serviceOnly && !strings.Contains(l.Tag, "service") {
			continue
		}
		if *showErrOnly && !strings.Contains(l.Tag, "err") {
			targets = append(targets, l)
			continue
		}
		targets = append(targets, l)
	}

	if len(targets) == 0 {
		fmt.Fprintln(os.Stderr, "No existing log files found.")
		fmt.Fprintln(os.Stderr, "Run `tokenlive logs --list` to check expected paths.")
		return 1
	}

	useColor := isTerminalOutput()

	if *follow {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		return followLogs(ctx, os.Stdout, targets, *lines, *showErrOnly, useColor)
	}

	// Normal tail N lines mode
	for i, target := range targets {
		if len(targets) > 1 {
			if i > 0 {
				fmt.Println()
			}
			printBanner(os.Stdout, target.Tag, target.Path, useColor)
		}

		linesRead := readLastNLines(target.Path, *lines)
		for _, line := range linesRead {
			if *showErrOnly && !isErrorLine(line) {
				continue
			}
			fmt.Println(formatLogLine(line, useColor))
		}
	}

	return 0
}

func checkErrors(logs []LogFileInfo) int {
	fmt.Println("=== TokenLive Log Health Check ===")
	totalErrors := 0
	existingCount := 0

	for _, l := range logs {
		if !l.Exists {
			continue
		}
		existingCount++
		errCount, recentErrs := scanErrors(l.Path, 1000)
		totalErrors += errCount

		if errCount == 0 {
			fmt.Printf("✓ [%s] %s: No errors found in last entries\n", l.Tag, l.Path)
		} else {
			fmt.Printf("✗ [%s] %s: Found %d error(s)!\n", l.Tag, l.Path, errCount)
			for _, line := range recentErrs {
				fmt.Printf("   └─ %s\n", truncateString(strings.TrimSpace(line), 120))
			}
		}
	}

	if existingCount == 0 {
		fmt.Println("⚠️  No log files found on system.")
		return 0
	}

	fmt.Println("==================================")
	if totalErrors > 0 {
		fmt.Printf("STATUS: FAILED (%d total error lines detected)\n", totalErrors)
		return 1
	}
	fmt.Println("STATUS: OK (No errors detected in logs)")
	return 0
}

func scanErrors(path string, maxLines int) (int, []string) {
	lines := readLastNLines(path, maxLines)
	var errLines []string
	count := 0
	for _, l := range lines {
		if isErrorLine(l) {
			count++
			if len(errLines) < 5 { // keep up to 5 samples
				errLines = append(errLines, l)
			}
		}
	}
	return count, errLines
}

func isErrorLine(line string) bool {
	upper := strings.ToUpper(line)
	for _, kw := range errorKeywords {
		if strings.Contains(upper, strings.ToUpper(kw)) {
			return true
		}
	}
	return false
}

func readLastNLines(path string, n int) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	st, err := file.Stat()
	if err != nil || st.Size() == 0 {
		return nil
	}

	// For smaller files, read all
	if st.Size() < 256*1024 {
		var lines []string
		scanner := bufio.NewScanner(file)
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, 1024*1024)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		if len(lines) > n {
			return lines[len(lines)-n:]
		}
		return lines
	}

	// Seek from end
	seekSize := int64(n * 512)
	if seekSize > st.Size() {
		seekSize = st.Size()
	}
	if seekSize < 32*1024 {
		seekSize = 32 * 1024
	}
	if seekSize > st.Size() {
		seekSize = st.Size()
	}

	_, _ = file.Seek(st.Size()-seekSize, io.SeekStart)
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var lines []string
	isFirst := true
	for scanner.Scan() {
		if isFirst && seekSize < st.Size() {
			isFirst = false
			continue
		}
		isFirst = false
		lines = append(lines, scanner.Text())
	}

	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}

func followLogs(ctx context.Context, w io.Writer, targets []LogFileInfo, initialN int, showErrOnly, useColor bool) int {
	type fileState struct {
		target LogFileInfo
		file   *os.File
		reader *bufio.Reader
		offset int64
		fi     os.FileInfo
	}

	states := make([]*fileState, 0, len(targets))
	for _, target := range targets {
		f, err := os.Open(target.Path)
		if err != nil {
			continue
		}
		printBanner(w, target.Tag, target.Path, useColor)

		initialLines := readLastNLines(target.Path, initialN)
		for _, l := range initialLines {
			if showErrOnly && !isErrorLine(l) {
				continue
			}
			fmt.Fprintln(w, formatLogLine(l, useColor))
		}

		fi, _ := f.Stat()
		offset := int64(0)
		if fi != nil {
			offset = fi.Size()
		}
		_, _ = f.Seek(offset, io.SeekStart)

		states = append(states, &fileState{
			target: target,
			file:   f,
			reader: bufio.NewReader(f),
			offset: offset,
			fi:     fi,
		})
	}
	defer func() {
		for _, s := range states {
			if s.file != nil {
				_ = s.file.Close()
			}
		}
	}()

	multi := len(states) > 1
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	emit := func(s *fileState, lineStr string) {
		if !showErrOnly || isErrorLine(lineStr) {
			if multi {
				fmt.Fprintf(w, "[%s] %s\n", s.target.Tag, formatLogLine(lineStr, useColor))
			} else {
				fmt.Fprintln(w, formatLogLine(lineStr, useColor))
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(w, "\nLog tailing stopped.")
			return 0
		case <-ticker.C:
			for _, s := range states {
				// If the handle is closed (file was removed/rotated on a prior
				// tick), try to reopen the path so monitoring resumes once the
				// file reappears.
				if s.file == nil {
					f, err := os.Open(s.target.Path)
					if err != nil {
						continue
					}
					s.file = f
					s.reader = bufio.NewReader(f)
					s.fi, _ = f.Stat()
					s.offset = 0
				}

				// Detect rotation/truncation by comparing the path's current
				// identity against the open handle. lumberjack rotates by
				// renaming the active file and creating a fresh one with the
				// same name (new inode); without reopening, we'd keep reading
				// the stale renamed file and miss every subsequent line.
				if pathFi, err := os.Stat(s.target.Path); err == nil {
					switch {
					case s.fi != nil && !os.SameFile(pathFi, s.fi):
						// Rotated: reopen the new file from the start.
						_ = s.file.Close()
						if f, err := os.Open(s.target.Path); err == nil {
							s.file = f
							s.reader = bufio.NewReader(f)
							s.fi, _ = f.Stat()
							s.offset = 0
						} else {
							s.file = nil
							s.fi = nil
							continue
						}
					case pathFi.Size() < s.offset:
						// Truncated in place: rewind to the beginning.
						_, _ = s.file.Seek(0, io.SeekStart)
						s.reader = bufio.NewReader(s.file)
						s.offset = 0
					}
				} else if os.IsNotExist(err) {
					// File removed; wait for it to be recreated.
					_ = s.file.Close()
					s.file = nil
					s.fi = nil
					continue
				}

				// Drain fully-buffered lines.
				for {
					line, err := s.reader.ReadString('\n')
					if line != "" {
						s.offset += int64(len(line))
						emit(s, strings.TrimRight(line, "\r\n"))
					}
					if err != nil {
						break
					}
				}
			}
		}
	}
}

// RunStatus handles `tokenlive status` subcommand.
func RunStatus(info Info, args []string) int {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	confPath := fs.String("conf", "", "gateway config path")
	dataDir := fs.String("data-dir", "", "data directory")
	_ = fs.Parse(args)

	resolvedConf := *confPath
	if resolvedConf == "" {
		resolvedConf = firstNonEmpty(info.DefaultConfigPath, "config/all-in-one.example.yml")
	}

	resolvedData := *dataDir
	if resolvedData == "" {
		resolvedData = firstNonEmpty(info.DefaultDataDir, "data")
	}

	host := "127.0.0.1"
	port := 2525
	if fileExists(resolvedConf) {
		v := config.NewConfig(resolvedConf)
		if h := v.GetString("http.host"); h != "" {
			host = h
		}
		if p := v.GetInt("http.port"); p != 0 {
			port = p
		}
	}

	url := fmt.Sprintf("http://%s:%d/health", host, port)

	fmt.Println("=== TokenLive Status ===")
	fmt.Printf("Version:     %s\n", firstNonEmpty(info.Version, "dev"))
	fmt.Printf("Config File: %s (%s)\n", resolvedConf, existsStr(resolvedConf))
	fmt.Printf("Data Dir:    %s (%s)\n", resolvedData, existsStr(resolvedData))
	fmt.Printf("Health URL:  %s\n", url)

	// Ping health endpoint
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Printf("Service:     DOWN (Failed to connect: %v)\n", err)
	} else {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusOK {
			fmt.Printf("Service:     RUNNING (HTTP 200 - %s)\n", strings.TrimSpace(string(body)))
		} else {
			fmt.Printf("Service:     UNHEALTHY (HTTP %d - %s)\n", resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}

	// Show log summary
	fmt.Println("\nLog Files:")
	logs := DiscoverLogs(info, resolvedConf, resolvedData)
	for _, l := range logs {
		if l.Exists {
			errCount, _ := scanErrors(l.Path, 200)
			errMsg := "0 errors"
			if errCount > 0 {
				errMsg = fmt.Sprintf("⚠️ %d errors", errCount)
			}
			fmt.Printf("  • [%-11s] %s (%s, %s)\n", l.Tag, l.Path, formatBytes(l.Size), errMsg)
		} else {
			fmt.Printf("  • [%-11s] %s (not created)\n", l.Tag, l.Path)
		}
	}
	fmt.Println("========================")

	return 0
}

func printBanner(w io.Writer, tag, path string, useColor bool) {
	banner := fmt.Sprintf("--- Log: [%s] %s ---", tag, path)
	if useColor {
		fmt.Fprintf(w, "\033[1;36m%s\033[0m\n", banner)
	} else {
		fmt.Fprintln(w, banner)
	}
}

func formatLogLine(line string, useColor bool) string {
	if !useColor {
		return line
	}
	upper := strings.ToUpper(line)
	if strings.Contains(upper, "ERROR") || strings.Contains(upper, "FATAL") || strings.Contains(upper, "PANIC") {
		return fmt.Sprintf("\033[1;31m%s\033[0m", line) // Red
	}
	if strings.Contains(upper, "WARN") {
		return fmt.Sprintf("\033[1;33m%s\033[0m", line) // Yellow
	}
	return line
}

func isTerminalOutput() bool {
	st, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (st.Mode() & os.ModeCharDevice) != 0
}

func existsStr(p string) string {
	if fileExists(p) {
		return "exists"
	}
	return "missing"
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return strconv.FormatInt(b, 10) + " B"
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
