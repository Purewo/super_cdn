package storage

import (
	"net/url"
	"path"
	"strings"
)

func IPFSCIDFromLocator(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if strings.HasPrefix(raw, "ipfs://") {
		if u, err := url.Parse(raw); err == nil {
			cid := strings.TrimSpace(u.Host)
			if cid == "" {
				rest := strings.Trim(u.Path, "/")
				cid, _, _ = strings.Cut(rest, "/")
			}
			return cid, cid != ""
		}
		rest := strings.Trim(strings.TrimPrefix(raw, "ipfs://"), "/")
		cid, _, _ := strings.Cut(rest, "/")
		cid, _, _ = strings.Cut(cid, "?")
		cid, _, _ = strings.Cut(cid, "#")
		return cid, cid != ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i], "ipfs") && parts[i+1] != "" {
			return parts[i+1], true
		}
	}
	return "", false
}

func IPFSGatewayURL(baseURL, cid string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	cid = strings.Trim(strings.TrimSpace(cid), "/")
	if baseURL == "" || cid == "" {
		return ""
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	u.Path = path.Join(u.Path, "ipfs", cid)
	return u.String()
}

func IPFSProviderPinIDFromLocator(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "ipfs" {
		return ""
	}
	return strings.TrimSpace(u.Query().Get("pinata_file_id"))
}

func PreserveIPFSProviderQuery(locator, previous string) string {
	locator = strings.TrimSpace(locator)
	if locator == "" {
		return strings.TrimSpace(previous)
	}
	u, err := url.Parse(locator)
	if err != nil || u.Scheme != "ipfs" {
		return locator
	}
	old, err := url.Parse(strings.TrimSpace(previous))
	if err != nil || old.Scheme != "ipfs" {
		return locator
	}
	q := u.Query()
	oldQuery := old.Query()
	for _, key := range []string{"pinata_group_id"} {
		if q.Get(key) == "" && oldQuery.Get(key) != "" {
			q.Set(key, oldQuery.Get(key))
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
