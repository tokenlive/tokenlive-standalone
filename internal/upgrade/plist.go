package upgrade

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// plistProgram strictly extracts the job program: Program if present, else
// ProgramArguments[0]. Parsing is a key/value state machine because plist XML
// has no schema: every direct dict <string> looks identical, so values must be
// paired with the preceding <key>. Anything unexpected (missing program,
// wrapper relative path) is an error so identity probes fail closed.
func plistProgram(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	var (
		program  string
		inArgs   bool
		args     []string
		pending  string
		dictSeen bool
		dictOpen int
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "dict":
				dictOpen++
				if dictOpen == 1 {
					dictSeen = true
				}
			case "key":
				if dictOpen == 1 {
					var key string
					if err := dec.DecodeElement(&key, &t); err != nil {
						return "", err
					}
					pending = key
				}
			case "string":
				if dictOpen == 1 && pending == "Program" {
					if err := dec.DecodeElement(&program, &t); err != nil {
						return "", err
					}
					pending = ""
				} else if inArgs {
					var value string
					if err := dec.DecodeElement(&value, &t); err != nil {
						return "", err
					}
					args = append(args, value)
				}
			case "array":
				if dictOpen == 1 && pending == "ProgramArguments" {
					inArgs = true
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "dict":
				dictOpen--
			case "array":
				if inArgs {
					inArgs = false
					pending = ""
				}
			}
		}
	}
	if !dictSeen {
		return "", fmt.Errorf("plist %s has no dict", filepath.Base(path))
	}
	if program == "" {
		if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
			return "", fmt.Errorf("plist %s has no program", filepath.Base(path))
		}
		program = args[0]
	}
	if !filepath.IsAbs(program) {
		return "", fmt.Errorf("plist %s program is not absolute", filepath.Base(path))
	}
	return program, nil
}

// taskPlist renders the one-shot upgrade job definition. Fields follow the
// launchd research candidate: independent label, RunAtLoad once, no keepalive,
// no timers, no UserName (user domain only), umask 077, bounded stop grace.
func taskPlist(label, executor string, args []string, workDir, stdoutLog string) ([]byte, error) {
	if label == "" || !filepath.IsAbs(executor) || !filepath.IsAbs(workDir) {
		return nil, fmt.Errorf("invalid task plist inputs")
	}
	for _, arg := range args {
		if strings.ContainsAny(arg, "<>&\"") {
			return nil, fmt.Errorf("task plist argument requires escaping")
		}
	}
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + label + `</string>
	<key>Program</key>
	<string>` + executor + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + executor + `</string>
`)
	for _, arg := range args {
		b.WriteString("		<string>" + arg + "</string>\n")
	}
	b.WriteString(`	</array>
	<key>WorkingDirectory</key>
	<string>` + workDir + `</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<false/>
	<key>LaunchOnlyOnce</key>
	<true/>
	<key>AbandonProcessGroup</key>
	<false/>
	<key>Umask</key>
	<integer>63</integer>
	<key>ExitTimeOut</key>
	<integer>15</integer>
	<key>StandardOutPath</key>
	<string>` + stdoutLog + `</string>
	<key>StandardErrorPath</key>
	<string>` + stdoutLog + `</string>
</dict>
</plist>
`)
	return b.Bytes(), nil
}
