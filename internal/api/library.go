package api

import (
	"context"
	"strconv"
)

type AlbumSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Artist    string `json:"artist"`
	SongCount int    `json:"songCount"`
}

type Album struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Songs []Song `json:"song"`
}

type Song struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Album    string `json:"album"`
	Artist   string `json:"artist"`
	Path     string `json:"path"`
	Duration int    `json:"duration"`
}

type albumList2 struct {
	Albums []AlbumSummary `json:"album"`
}

func (c *Client) Albums(ctx context.Context, offset, size int) ([]AlbumSummary, error) {
	var env subsonicEnvelope
	err := c.call(ctx, "getAlbumList2", map[string]string{
		"type":   "alphabeticalByName",
		"offset": strconv.Itoa(offset),
		"size":   strconv.Itoa(size),
	}, &env)
	if err != nil {
		return nil, err
	}
	return env.Response.AlbumList2.Albums, nil
}

func (c *Client) Album(ctx context.Context, id string) (Album, error) {
	var env subsonicEnvelope
	err := c.call(ctx, "getAlbum", map[string]string{"id": id}, &env)
	if err != nil {
		return Album{}, err
	}
	return env.Response.Album, nil
}
