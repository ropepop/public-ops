package web

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"strings"
	"time"

	appversion "satiksmebot/internal/version"
)

type releaseInfo struct {
	Commit         string
	BuildTime      string
	Dirty          string
	Instance       string
	AppJSHash      string
	AppCSSHash     string
	LiveClientHash string
	assetHash      map[string]string
}

func newReleaseInfo(static fs.FS) (releaseInfo, error) {
	appJSHash, err := hashStaticAsset(static, "app.js")
	if err != nil {
		return releaseInfo{}, err
	}
	appCSSHash, err := hashStaticAsset(static, "app.css")
	if err != nil {
		return releaseInfo{}, err
	}
	liveClientHash, err := hashOptionalStaticAsset(static, "live-client.js")
	if err != nil {
		return releaseInfo{}, err
	}
	instanceID, err := randomInstanceID()
	if err != nil {
		return releaseInfo{}, err
	}
	info := releaseInfo{
		Commit:         strings.TrimSpace(appversion.Commit),
		BuildTime:      strings.TrimSpace(appversion.BuildTime),
		Dirty:          strings.TrimSpace(appversion.Dirty),
		Instance:       instanceID,
		AppJSHash:      appJSHash,
		AppCSSHash:     appCSSHash,
		LiveClientHash: liveClientHash,
		assetHash: map[string]string{
			"app.js":  appJSHash,
			"app.css": appCSSHash,
		},
	}
	if liveClientHash != "" {
		info.assetHash["live-client.js"] = liveClientHash
	}
	return info, nil
}

func hashStaticAsset(static fs.FS, name string) (string, error) {
	body, err := fs.ReadFile(static, name)
	if err != nil {
		return "", fmt.Errorf("read static asset %s: %w", name, err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func hashOptionalStaticAsset(static fs.FS, name string) (string, error) {
	body, err := fs.ReadFile(static, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read static asset %s: %w", name, err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func randomInstanceID() (string, error) {
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", fmt.Errorf("read instance entropy: %w", err)
	}
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(entropy[:]), nil
}

func (r releaseInfo) AssetURL(basePath string, assetPath string) string {
	trimmedBase := strings.TrimRight(basePath, "/")
	version := r.assetHash[assetPath]
	base := fmt.Sprintf("%s/assets/%s", trimmedBase, assetPath)
	if version == "" {
		return base
	}
	return base + "?v=" + url.QueryEscape(version)
}

func (r releaseInfo) AssetHash(assetPath string) string {
	return r.assetHash[assetPath]
}
