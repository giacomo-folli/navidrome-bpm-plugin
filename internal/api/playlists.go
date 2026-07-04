package api

import (
	"context"
	"net/url"
	"strconv"
)

type Playlist struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Entry []Song `json:"entry"`
}

type playlists struct {
	Playlist []Playlist `json:"playlist"`
}

func (c *Client) Playlists(ctx context.Context) ([]Playlist, error) {
	var env subsonicEnvelope
	if err := c.call(ctx, "getPlaylists", nil, &env); err != nil {
		return nil, err
	}
	return env.Response.Playlists.Playlist, nil
}

func (c *Client) CreatePlaylist(ctx context.Context, name string, songIDs []string) (Playlist, error) {
	query := url.Values{}
	query.Set("name", name)
	for _, id := range songIDs {
		query.Add("songId", id)
	}
	var env subsonicEnvelope
	if err := c.callValues(ctx, "createPlaylist", query, &env); err != nil {
		return Playlist{}, err
	}
	return env.Response.Playlist, nil
}

func (c *Client) Playlist(ctx context.Context, id string) (Playlist, error) {
	var env subsonicEnvelope
	if err := c.call(ctx, "getPlaylist", map[string]string{"id": id}, &env); err != nil {
		return Playlist{}, err
	}
	return env.Response.Playlist, nil
}

func (c *Client) UpdatePlaylist(ctx context.Context, id string, removeIndexes []int, addSongIDs []string) error {
	query := url.Values{}
	query.Set("playlistId", id)
	for _, idx := range removeIndexes {
		query.Add("songIndexToRemove", strconv.Itoa(idx))
	}
	for _, songID := range addSongIDs {
		query.Add("songIdToAdd", songID)
	}
	var env subsonicEnvelope
	return c.callValues(ctx, "updatePlaylist", query, &env)
}

func (c *Client) DeletePlaylist(ctx context.Context, id string) error {
	var env subsonicEnvelope
	return c.call(ctx, "deletePlaylist", map[string]string{"id": id}, &env)
}
