package version

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/uozi-tech/cosy"
)

type TCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message   string `json:"message"`
		Committer struct {
			Date time.Time `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
}

func getDevBuild() (data TRelease, err error) {
	client, err := NewHTTPClient()
	if err != nil {
		return
	}
	body, err := getAPIBody(client, GetGithubDevCommitAPIUrl())
	if err != nil {
		return
	}
	commit := TCommit{}
	err = json.Unmarshal(body, &commit)
	if err != nil {
		return
	}
	if len(commit.SHA) < 7 {
		err = ErrInvalidCommitSHA
		return
	}
	shortSHA := commit.SHA[:7]

	body, err = getAPIBody(client, fmt.Sprintf("%sdev-builds", CloudflareWorkerAPI))
	if err != nil {
		return
	}

	assets := make([]TReleaseAsset, 0)
	err = json.Unmarshal(body, &assets)
	if err != nil {
		return
	}

	data = TRelease{
		TagName:     "sha-" + shortSHA,
		Name:        "sha-" + shortSHA,
		Body:        commit.Commit.Message,
		Type:        ReleaseTypeDev,
		PublishedAt: commit.Commit.Committer.Date,
		Assets:      assets,
		HTMLURL:     fmt.Sprintf("https://github.com/0xJacky/Nginx-UI/commit/%s", commit.SHA),
	}

	return
}

// getAPIBody fetches url and returns its body, treating any non-200 status
// as ErrReleaseAPIFailed like the stable and prerelease lookups do.
func getAPIBody(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, cosy.WrapErrorWithParams(ErrReleaseAPIFailed, string(body))
	}
	return body, nil
}
