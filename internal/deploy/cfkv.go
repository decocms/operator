package deploy

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// kvLiveKey is the pointer key naming the currently-live deployment id. It MUST
// match `LIVE_KEY` in `@decocms/blocks` (`packages/blocks/src/cms/blockSource.ts`)
// — the runtime and the sync scripts key content by deployment id and record the
// live one here after a code deploy activates.
const kvLiveKey = "index:live"

// fetchKVLiveID reads the index:live pointer from a site's KV namespace via the
// Cloudflare REST API, returning the live deployment id. Returns ("", nil) when
// the pointer is absent (404) — no code deploy has set it yet, so there is no
// live version to fast-deploy content onto.
func fetchKVLiveID(ctx context.Context, accountID, apiToken, namespaceID string) (string, error) {
	url := fmt.Sprintf(
		"https://api.cloudflare.com/client/v4/accounts/%s/storage/kv/namespaces/%s/values/%s",
		accountID, namespaceID, kvLiveKey,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode == http.StatusNotFound {
		return "", nil
	}
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("KV GET %s failed: %d %s", kvLiveKey, res.StatusCode, string(body))
	}
	return string(body), nil
}
