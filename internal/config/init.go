package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed initial.toml
var initialConfig string

// DefaultPath follows the user's configuration directory, independent of cwd.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("无法确定用户配置目录，请使用 --config 指定路径: %w", err)
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("用户配置目录必须为绝对路径，请检查 XDG_CONFIG_HOME 或使用 --config")
	}
	return filepath.Join(dir, "buptwc", "config.toml"), nil
}

// Initialize creates a private configuration file, never replacing an existing file.
// It does not create a session, database or credentials.
func Initialize(path string) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("配置已存在，未覆盖 %s: %w", path, err)
		}
		return err
	}
	defer func() {
		f.Close()
		if err != nil {
			os.Remove(path)
		}
	}()
	if _, err = f.WriteString(initialConfig); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return f.Close()
}
