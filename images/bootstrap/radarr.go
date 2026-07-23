package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/tomerhanochi/homelab/bootstrap/radarrapi"
)

// newRadarrClient adapts the generated Radarr client to arrClient. Every request
// carries the X-Api-Key header.
func newRadarrClient(base, apiKey string) (arrClient, error) {
	c, err := radarrapi.NewClientWithResponses(base,
		radarrapi.WithHTTPClient(httpClient),
		radarrapi.WithRequestEditorFn(func(ctx context.Context, req *http.Request) error {
			req.Header.Set("X-Api-Key", apiKey)
			return nil
		}),
	)
	if err != nil {
		return nil, err
	}
	return &radarrClient{c: c}, nil
}

type radarrClient struct {
	c *radarrapi.ClientWithResponses
}

func (a *radarrClient) RootFolders(ctx context.Context) (map[string]bool, error) {
	resp, err := a.c.GetApiV3RootfolderWithResponse(ctx)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("list root folders: status %d: %s", resp.StatusCode(), resp.Body)
	}
	have := make(map[string]bool, len(*resp.JSON200))
	for _, r := range *resp.JSON200 {
		if r.Path != nil {
			have[*r.Path] = true
		}
	}
	return have, nil
}

func (a *radarrClient) AddRootFolder(ctx context.Context, path string) error {
	resp, err := a.c.PostApiV3RootfolderWithResponse(ctx, radarrapi.RootFolderResource{Path: &path})
	if err != nil {
		return err
	}
	if !ok(resp.StatusCode()) {
		return fmt.Errorf("add root folder %q: status %d: %s", path, resp.StatusCode(), resp.Body)
	}
	return nil
}

func (a *radarrClient) QBittorrentDownloadClient(ctx context.Context, name string) (*arrDownloadClient, error) {
	list, err := a.c.GetApiV3DownloadclientWithResponse(ctx)
	if err != nil {
		return nil, err
	}
	if list.JSON200 == nil {
		return nil, fmt.Errorf("list download clients: status %d: %s", list.StatusCode(), list.Body)
	}
	var res *radarrapi.DownloadClientResource
	for i := range *list.JSON200 {
		if r := &(*list.JSON200)[i]; r.Name != nil && *r.Name == name {
			res = r
			break
		}
	}
	if res == nil {
		schema, err := a.c.GetApiV3DownloadclientSchemaWithResponse(ctx)
		if err != nil {
			return nil, err
		}
		if schema.JSON200 == nil {
			return nil, fmt.Errorf("get download client schema: status %d: %s", schema.StatusCode(), schema.Body)
		}
		for i := range *schema.JSON200 {
			if r := &(*schema.JSON200)[i]; r.Implementation != nil && *r.Implementation == "QBittorrent" {
				res = r
				break
			}
		}
		if res == nil {
			return nil, fmt.Errorf("QBittorrent implementation not found in download client schema")
		}
	}

	return &arrDownloadClient{
		setName:            func(s string) { res.Name = &s },
		setEnable:          func(b bool) { res.Enable = &b },
		setRemoveCompleted: func(b bool) { res.RemoveCompletedDownloads = &b },
		setField: func(name string, value any) {
			if res.Fields == nil {
				return
			}
			for i := range *res.Fields {
				if f := &(*res.Fields)[i]; f.Name != nil && *f.Name == name {
					f.Value = value
					return
				}
			}
		},
		snapshot: func() string { return mustJSON(res) },
		save: func(ctx context.Context) error {
			if res.Id != nil {
				resp, err := a.c.PutApiV3DownloadclientIdWithResponse(ctx, *res.Id, nil, *res)
				if err != nil {
					return err
				}
				if !ok(resp.StatusCode()) {
					return fmt.Errorf("update download client: status %d: %s", resp.StatusCode(), resp.Body)
				}
				return nil
			}
			resp, err := a.c.PostApiV3DownloadclientWithResponse(ctx, nil, *res)
			if err != nil {
				return err
			}
			if !ok(resp.StatusCode()) {
				return fmt.Errorf("create download client: status %d: %s", resp.StatusCode(), resp.Body)
			}
			return nil
		},
	}, nil
}
