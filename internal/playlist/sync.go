package playlist

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"

	"github.com/giacomo-folli/navidrome-bpm-plugin/internal/api"
	"github.com/giacomo-folli/navidrome-bpm-plugin/internal/worker"
)

type API interface {
	Playlists(ctx context.Context) ([]api.Playlist, error)
	CreatePlaylist(ctx context.Context, name string, songIDs []string) (api.Playlist, error)
	Playlist(ctx context.Context, id string) (api.Playlist, error)
	UpdatePlaylist(ctx context.Context, id string, removeIndexes []int, addSongIDs []string) error
	DeletePlaylist(ctx context.Context, id string) error
	StartScan(ctx context.Context) error
}

type Options struct {
	BucketSize        int
	Minimum           int
	Maximum           int
	DeleteEmpty       bool
	RescanAfterTags   bool
	IncludeOutOfRange bool
}

type Syncer struct {
	api    API
	opt    Options
	logger *slog.Logger
}

func NewSyncer(api API, opt Options, logger *slog.Logger) *Syncer {
	if opt.BucketSize <= 0 {
		opt.BucketSize = 10
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Syncer{api: api, opt: opt, logger: logger}
}

func Bucket(bpm float64, size int) int {
	if size <= 0 {
		size = 10
	}
	return int(math.Round(bpm/float64(size))) * size
}

func Name(bucket int) string {
	return fmt.Sprintf("%03dbpm", bucket)
}

func (s *Syncer) Sync(ctx context.Context, results []worker.Result) error {
	s.logger.Info("Synchronizing BPM playlists...", "tracks_with_bpm", len(results), "bucket_size", s.opt.BucketSize, "minimum", s.opt.Minimum, "maximum", s.opt.Maximum)

	existing, err := s.api.Playlists(ctx)
	if err != nil {
		return err
	}
	s.logger.Info("loaded existing playlists", "count", len(existing))

	byName := map[string]api.Playlist{}
	for _, pl := range existing {
		byName[pl.Name] = pl
	}

	targets := map[int][]string{}
	outOfRange := 0
	for _, r := range results {
		bucket := Bucket(r.BPM, s.opt.BucketSize)
		if !s.opt.IncludeOutOfRange && (bucket < s.opt.Minimum || bucket > s.opt.Maximum) {
			outOfRange++
			continue
		}
		targets[bucket] = append(targets[bucket], r.SongID)
	}
	for _, ids := range targets {
		sort.Strings(ids)
	}
	s.logger.Info("grouped tracks into BPM buckets", "active_buckets", len(targets), "out_of_range", outOfRange)

	created := 0
	updated := 0
	unchanged := 0
	deleted := 0
	empty := 0
	for bucket := s.opt.Minimum; bucket <= s.opt.Maximum; bucket += s.opt.BucketSize {
		name := Name(bucket)
		want := targets[bucket]
		pl, ok := byName[name]
		if len(want) == 0 {
			empty++
			if ok && s.opt.DeleteEmpty {
				s.logger.Info("deleting empty BPM playlist", "playlist", name, "id", pl.ID)
				if err := s.api.DeletePlaylist(ctx, pl.ID); err != nil {
					return err
				}
				deleted++
			}
			continue
		}
		if !ok {
			s.logger.Info("creating BPM playlist", "playlist", name, "tracks", len(want))
			if _, err := s.api.CreatePlaylist(ctx, name, want); err != nil {
				return err
			}
			created++
			continue
		}
		current, err := s.api.Playlist(ctx, pl.ID)
		if err != nil {
			return err
		}
		currentIDs := playlistSongIDs(current)
		if equalStringSlices(currentIDs, want) {
			s.logger.Info("BPM playlist already synchronized", "playlist", name, "tracks", len(want))
			unchanged++
			continue
		}
		remove := make([]int, len(current.Entry))
		for i := range current.Entry {
			remove[i] = len(current.Entry) - 1 - i
		}
		s.logger.Info("replacing BPM playlist contents", "playlist", name, "previous_tracks", len(current.Entry), "tracks", len(want), "remove", len(remove), "add", len(want))
		if err := s.api.UpdatePlaylist(ctx, current.ID, remove, want); err != nil {
			return err
		}
		updated++
	}
	if s.opt.RescanAfterTags {
		s.logger.Info("requesting Navidrome rescan after metadata writes")
		return s.api.StartScan(ctx)
	}
	s.logger.Info("playlist synchronization finished", "created", created, "updated", updated, "unchanged", unchanged, "deleted", deleted, "empty_buckets", empty)
	return nil
}

func playlistSongIDs(pl api.Playlist) []string {
	ids := make([]string, 0, len(pl.Entry))
	for _, song := range pl.Entry {
		ids = append(ids, song.ID)
	}
	return ids
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
