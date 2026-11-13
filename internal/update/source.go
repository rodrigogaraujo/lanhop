package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
)

const (
	ProjectReleases = "https://github.com/rodrigogaraujo/lanhop/releases"

	checksumsName = "SHA256SUMS"
	signatureName = "SHA256SUMS.sig"
	updateName    = "UPDATE"

	smallFileLimit = 4 << 10
	archiveLimit   = 100 << 20
)

var (
	ErrNotSigned       = errors.New("update: the checksums of the release do not carry a valid signature of the project")
	ErrNotListed       = errors.New("update: the checksums of the release do not list the file")
	ErrChanged         = errors.New("update: a file of the release does not match its checksum")
	ErrOtherVersion    = errors.New("update: the release says it is another version than the one asked for")
	ErrTooLarge        = errors.New("update: a file of the release is larger than lanhop accepts")
	ErrNoLatest        = errors.New("update: GitHub did not say which release is the latest")
	ErrUnexpectedReply = errors.New("update: GitHub answered with something lanhop does not expect")
	ErrNotHTTPS        = errors.New("update: GitHub redirected to an address that is not HTTPS")
)

const redirectLimit = 10

type Source struct {
	Releases string
	Client   *http.Client
	Keys     []ed25519.PublicKey
	CheckApp func(app string) error
}

type release struct {
	version   Version
	from      Version
	checksums map[string]string
}

func (s Source) Latest(ctx context.Context) (Version, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Releases+"/latest", nil)
	if err != nil {
		return Version{}, err
	}
	client := *s.client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return Version{}, err
	}
	response.Body.Close()
	location, err := response.Location()
	if response.StatusCode/100 != 3 || err != nil {
		return Version{}, fmt.Errorf("%w: status %d", ErrNoLatest, response.StatusCode)
	}
	tag := path.Base(location.Path)
	if path.Dir(location.Path) != path.Join(request.URL.Path, "..", "tag") {
		return Version{}, fmt.Errorf("%w: it sent to %s", ErrNoLatest, location)
	}
	version, err := ParseVersion(tag)
	if err != nil {
		return Version{}, fmt.Errorf("%w: %w", ErrNoLatest, err)
	}
	return version, nil
}

func (s Source) client() *http.Client {
	base := http.DefaultClient
	if s.Client != nil {
		base = s.Client
	}
	client := *base
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= redirectLimit {
			return fmt.Errorf("%w: more than %d redirects", ErrUnexpectedReply, redirectLimit)
		}
		if via[0].URL.Scheme == "https" && next.URL.Scheme != "https" {
			return fmt.Errorf("%w: %s", ErrNotHTTPS, next.URL)
		}
		return nil
	}
	return &client
}

func (s Source) fetch(ctx context.Context, version Version, name string, limit int64) ([]byte, error) {
	address := fmt.Sprintf("%s/download/%s/%s", s.Releases, version, name)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := s.client().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s gave status %d", ErrUnexpectedReply, name, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: %s", ErrTooLarge, name)
	}
	return body, nil
}

func (s Source) signedRelease(ctx context.Context, want Version) (release, error) {
	checksums, err := s.fetch(ctx, want, checksumsName, smallFileLimit)
	if err != nil {
		return release{}, err
	}
	signature, err := s.fetch(ctx, want, signatureName, ed25519.SignatureSize)
	if err != nil {
		return release{}, err
	}
	if !s.signedByTheProject(checksums, signature) {
		return release{}, ErrNotSigned
	}
	listed, err := parseChecksums(checksums)
	if err != nil {
		return release{}, err
	}
	update, err := s.fetch(ctx, want, updateName, smallFileLimit)
	if err != nil {
		return release{}, err
	}
	if err := matches(listed, updateName, update); err != nil {
		return release{}, err
	}
	version, from, err := parseUpdate(update)
	if err != nil {
		return release{}, err
	}
	if version != want {
		return release{}, fmt.Errorf("%w: asked for %s, it says %s", ErrOtherVersion, want, version)
	}
	return release{version: version, from: from, checksums: listed}, nil
}

func (s Source) signedByTheProject(message, signature []byte) bool {
	if len(signature) != ed25519.SignatureSize {
		return false
	}
	for _, key := range s.Keys {
		if len(key) == ed25519.PublicKeySize && ed25519.Verify(key, message, signature) {
			return true
		}
	}
	return false
}

func parseChecksums(content []byte) (map[string]string, error) {
	listed := map[string]string{}
	lines := bufio.NewScanner(bytes.NewReader(content))
	for lines.Scan() {
		fields := strings.Fields(lines.Text())
		if len(fields) != 2 {
			continue
		}
		listed[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	return listed, lines.Err()
}

func matches(listed map[string]string, name string, content []byte) error {
	expected, found := listed[name]
	if !found {
		return fmt.Errorf("%w: %s", ErrNotListed, name)
	}
	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != expected {
		return fmt.Errorf("%w: %s", ErrChanged, name)
	}
	return nil
}

func parseUpdate(content []byte) (version, from Version, err error) {
	var versionText, fromText string
	for _, line := range strings.Split(string(content), "\n") {
		key, value, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch key {
		case "version":
			versionText = value
		case "from":
			fromText = value
		}
	}
	if version, err = ParseVersion(versionText); err != nil {
		return Version{}, Version{}, fmt.Errorf("update: the UPDATE file of the release: %w", err)
	}
	if from, err = ParseVersion(fromText); err != nil {
		return Version{}, Version{}, fmt.Errorf("update: the UPDATE file of the release: %w", err)
	}
	return version, from, nil
}
