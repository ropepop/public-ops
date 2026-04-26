package web

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"strings"
	"time"

	appversion "subscriptionbot/internal/version"
)

type releaseInfo struct {
	Commit     string
	BuildTime  string
	Dirty      string
	Instance   string
	AppJSHash  string
	AppCSSHash string
	assetHash  map[string]string
}

func newReleaseInfo(static fs.FS) (releaseInfo, error) {
	assetHashes, err := hashStaticAssets(static)
	if err != nil {
		return releaseInfo{}, err
	}
	instanceID, err := randomInstanceID()
	if err != nil {
		return releaseInfo{}, err
	}
	return releaseInfo{
		Commit:     strings.TrimSpace(appversion.Commit),
		BuildTime:  strings.TrimSpace(appversion.BuildTime),
		Dirty:      strings.TrimSpace(appversion.Dirty),
		Instance:   instanceID,
		AppJSHash:  assetHashes["app.js"],
		AppCSSHash: assetHashes["app.css"],
		assetHash:  assetHashes,
	}, nil
}

func hashStaticAsset(static fs.FS, name string) (string, error) {
	body, err := fs.ReadFile(static, name)
	if err != nil {
		return "", fmt.Errorf("read static asset %s: %w", name, err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func hashStaticAssets(static fs.FS) (map[string]string, error) {
	hashes := make(map[string]string)
	if err := fs.WalkDir(static, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		sum, err := hashStaticAsset(static, path)
		if err != nil {
			return err
		}
		hashes[path] = sum
		return nil
	}); err != nil {
		return nil, fmt.Errorf("hash static assets: %w", err)
	}
	return hashes, nil
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
	if trimmedBase == "" {
		base = "/assets/" + assetPath
	}
	if version == "" {
		return base
	}
	return base + "?v=" + url.QueryEscape(version)
}

func (r releaseInfo) AssetHash(assetPath string) string {
	return r.assetHash[assetPath]
}
