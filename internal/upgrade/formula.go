package upgrade

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/tokenlive/tokenlive-admin/pkg/productversion"
)

const maxFormulaBytes = 64 << 10

// formulaAPIURL is the same bounded, controlled source the update checker
// uses: the canonical tap Formula on GitHub, not a random mirror.
const formulaAPIURL = "https://api.github.com/repos/tokenlive/homebrew-tokenlive/contents/Formula/tokenlive.rb"

var (
	reClass     = regexp.MustCompile(`(?m)^class\s+Tokenlive\s*<\s*Formula\s*$`)
	reVersion   = regexp.MustCompile(`(?m)^[ \t]*version[ \t]+"([^"\r\n]+)"[ \t]*(?:#[^\r\n]*)?\r?$`)
	reURL       = regexp.MustCompile(`(?m)^[ \t]*url[ \t]+"([^"\r\n]+)"`)
	reSHA       = regexp.MustCompile(`(?m)^[ \t]*sha256[ \t]+"[a-f0-9]{64}"[ \t]*(?:#[^\r\n]*)?\r?$`)
	reForbidden = regexp.MustCompile(`(?m)^[ \t]*(depends_on|uses_from_macos|head|go_resource|resource)\b`)
)

// ValidateFormula enforces the reviewed, dependency-free Formula template that
// the first phase accepts. The canonical template declares one version and one
// url+sha256 pair per architecture block (currently two); every url must point
// at the controlled standalone release host. Any new structural behavior must
// ship with an adapter review first. It returns the canonical stable version
// and the raw declared version (which names the Cellar keg).
func ValidateFormula(body []byte) (stable, raw string, err error) {
	if len(body) > maxFormulaBytes {
		return "", "", fmt.Errorf("Formula too large")
	}
	if !utf8.Valid(body) {
		return "", "", fmt.Errorf("Formula is not valid UTF-8")
	}
	if classes := reClass.FindAll(body, -1); len(classes) != 1 {
		return "", "", fmt.Errorf("expected exactly one Tokenlive Formula class")
	}
	if matches := reForbidden.FindAllSubmatchIndex(body, -1); len(matches) != 0 {
		return "", "", fmt.Errorf("Formula contains rejected directives (depends_on/head/resource)")
	}
	urls := reURL.FindAllSubmatch(body, -1)
	if len(urls) < 1 || len(urls) > 2 {
		return "", "", fmt.Errorf("expected one url per architecture block, got %d", len(urls))
	}
	for _, match := range urls {
		parsed, err := url.Parse(string(match[1]))
		if err != nil || parsed.Scheme != "https" ||
			parsed.Host != "github.com" ||
			!strings.HasPrefix(parsed.Path, "/tokenlive/tokenlive-standalone/") {
			return "", "", fmt.Errorf("Formula url is not the controlled release host")
		}
	}
	if shas := reSHA.FindAllSubmatch(body, -1); len(shas) != len(urls) {
		return "", "", fmt.Errorf("expected one sha256 per url, got %d for %d urls", len(shas), len(urls))
	}
	versions := reVersion.FindAllSubmatch(body, -1)
	if len(versions) != 1 {
		return "", "", fmt.Errorf("expected exactly one version declaration")
	}
	rawVersion := string(versions[0][1])
	stable, ok := productversion.StableVersion(rawVersion)
	if !ok {
		return "", "", fmt.Errorf("Formula version is not a stable release")
	}
	return stable, rawVersion, nil
}

// FetchFormula downloads the canonical Formula bytes and validates them.
// The returned bytes become the fixed install target snapshot; stable is the
// canonical version, raw the declared version naming the Cellar keg.
func FetchFormula(ctx context.Context, client *http.Client) (body []byte, stable, raw string, err error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, formulaAPIURL, nil)
	if err != nil {
		return nil, "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "tokenlive-standalone-upgrade")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", "", fmt.Errorf("fetch Formula: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", "", fmt.Errorf("fetch Formula: status %d", resp.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxFormulaBytes*2))
	if err != nil {
		return nil, "", "", err
	}
	var contents struct {
		Type     string  `json:"type"`
		Encoding string  `json:"encoding"`
		Content  *string `json:"content"`
	}
	if err := json.Unmarshal(payload, &contents); err != nil {
		return nil, "", "", errors.New("invalid Formula contents response")
	}
	if contents.Type != "file" || contents.Encoding != "base64" || contents.Content == nil {
		return nil, "", "", errors.New("invalid Formula contents response")
	}
	decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(*contents.Content)))
	if err != nil {
		return nil, "", "", err
	}
	stableVersion, rawVersion, err := ValidateFormula(decoded)
	if err != nil {
		return nil, "", "", err
	}
	return decoded, stableVersion, rawVersion, nil
}

// ErrNoCandidate mirrors the update checker's contract for "no upgrade".
var ErrNoCandidate = errors.New("no stable candidate")
