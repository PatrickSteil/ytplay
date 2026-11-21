package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

type config struct {
	path     string
	autoplay bool
}

func loadConfig() (config, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return config{}, err
		}
		dir = filepath.Join(home, ".config")
	}
	path := filepath.Join(dir, "ytplay", "config.toml")
	values, err := readConfig(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return config{}, err
		}
		var f *os.File
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		switch {
		case err == nil:
			_, writeErr := f.WriteString("autoplay = false\n")
			return config{path: path}, errors.Join(writeErr, f.Close())
		case errors.Is(err, os.ErrExist):
			values, err = readConfig(path)
		}
	}
	if err != nil {
		return config{}, err
	}
	on, _ := values["autoplay"].(bool)
	return config{path: path, autoplay: on}, nil
}

func readConfig(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	values := make(map[string]any)
	if err := toml.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if value, ok := values["autoplay"]; ok {
		if _, ok := value.(bool); !ok {
			return nil, fmt.Errorf("%s: autoplay must be true or false", path)
		}
	}
	return values, nil
}

func (c config) saveAutoplay(on bool) error {
	path, err := filepath.EvalSymlinks(c.path)
	if errors.Is(err, os.ErrNotExist) {
		path = c.path
	} else if err != nil {
		return err
	}
	values, err := readConfig(path)
	if errors.Is(err, os.ErrNotExist) {
		values = make(map[string]any)
	} else if err != nil {
		return err
	}
	values["autoplay"] = on
	data, err := toml.Marshal(values)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
