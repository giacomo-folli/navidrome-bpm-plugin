package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Navidrome struct {
		URL      string
		Username string
		Password string
	}
	MusicDir string
	Cache    struct {
		Path string
	}
	Analysis struct {
		Detector string
		Workers  int
	}
	Playlist struct {
		BucketSize        int
		Minimum           int
		Maximum           int
		DeleteEmpty       bool
		IncludeOutOfRange bool
	}
	Scan struct {
		Interval time.Duration
	}
	Metadata struct {
		WriteTags        bool
		RescanAfterWrite bool
	}
}

func Load() (Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("./config")
	v.AddConfigPath("/config")
	v.SetEnvPrefix("NBDPM")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	v.SetDefault("cache.path", "/config/cache.sqlite")
	v.SetDefault("analysis.detector", "essentia")
	v.SetDefault("analysis.workers", 6)
	v.SetDefault("playlist.bucketSize", 10)
	v.SetDefault("playlist.minimum", 60)
	v.SetDefault("playlist.maximum", 220)
	v.SetDefault("playlist.deleteEmpty", false)
	v.SetDefault("playlist.includeOutOfRange", false)
	v.SetDefault("scan.interval", "30m")
	v.SetDefault("metadata.writeTags", false)
	v.SetDefault("metadata.rescanAfterWrite", false)

	for _, key := range []string{
		"navidrome.url",
		"navidrome.username",
		"navidrome.password",
		"musicDir",
		"cache.path",
		"analysis.detector",
		"analysis.workers",
		"playlist.bucketSize",
		"playlist.minimum",
		"playlist.maximum",
		"playlist.deleteEmpty",
		"playlist.includeOutOfRange",
		"scan.interval",
		"metadata.writeTags",
		"metadata.rescanAfterWrite",
	} {
		if err := v.BindEnv(key); err != nil {
			return Config{}, err
		}
	}

	if err := v.ReadInConfig(); err != nil {
		var cfg Config
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return cfg, err
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return cfg, err
	}
	if cfg.Navidrome.URL == "" || cfg.Navidrome.Username == "" || cfg.Navidrome.Password == "" {
		return cfg, fmt.Errorf("navidrome url, username, and password are required")
	}
	if cfg.MusicDir == "" {
		return cfg, fmt.Errorf("musicDir is required")
	}
	if cfg.Analysis.Workers < 1 {
		cfg.Analysis.Workers = 1
	}
	return cfg, nil
}
