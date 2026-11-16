package upgrade

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Restart struct{ ID, Directory, Executable string }
type replacement struct {
	Target  string `json:"target"`
	Staged  string `json:"staged"`
	Backup  string `json:"backup"`
	Existed bool   `json:"existed"`
	Mode    uint32 `json:"mode"`
}
type manifest struct {
	Files []replacement `json:"files"`
}

func (r Restart) RecordFailure(err error) error {
	return os.WriteFile(filepath.Join(r.Directory, "restart-error"), []byte(err.Error()), 0o600)
}

func copyFile(source, target string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Chmod(mode)
	}
	if err == nil {
		err = out.Sync()
	}
	return errors.Join(err, out.Close())
}

func stagedCopy(source, target string, mode os.FileMode) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(target), ".agentuse-upgrade-*")
	if err != nil {
		return "", err
	}
	name := file.Name()
	in, openErr := os.Open(source)
	if openErr != nil {
		file.Close()
		os.Remove(name)
		return "", openErr
	}
	_, err = io.Copy(file, in)
	in.Close()
	if err == nil {
		err = file.Chmod(mode)
	}
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// Prepare places every candidate beside its destination before any replacement,
// so read-only installations and cross-filesystem staging fail before downtime.
func (r Restart) Prepare(dist string) (result error) {
	if err := os.MkdirAll(dist, 0o755); err != nil {
		return err
	}
	var journal manifest
	defer func() {
		if result != nil {
			for _, file := range journal.Files {
				os.Remove(file.Staged)
			}
		}
	}()
	for index, name := range artifactNames() {
		target := filepath.Join(dist, name)
		if index == 4 {
			target = r.Executable
		}
		entry := replacement{Target: target, Backup: filepath.Join(r.Directory, fmt.Sprintf("previous-%d", index)), Mode: 0o755}
		info, err := os.Lstat(target)
		if err == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("升级目标不是普通文件: %s", target)
			}
			entry.Existed = true
			entry.Mode = uint32(info.Mode().Perm())
			if err = copyFile(target, entry.Backup, info.Mode().Perm()); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		entry.Staged, err = stagedCopy(filepath.Join(r.Directory, name), target, os.FileMode(entry.Mode)|0o100)
		if err != nil {
			return err
		}
		journal.Files = append(journal.Files, entry)
	}
	encoded, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(r.Directory, "replacement.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(encoded)
	if err == nil {
		err = file.Sync()
	}
	return errors.Join(err, file.Close())
}

func (r Restart) journal() (manifest, error) {
	var out manifest
	b, err := os.ReadFile(filepath.Join(r.Directory, "replacement.json"))
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}
func (r Restart) Apply() error {
	journal, err := r.journal()
	if err != nil {
		return err
	}
	for _, file := range journal.Files {
		if err = os.Rename(file.Staged, file.Target); err != nil {
			return fmt.Errorf("替换 %s 失败: %w", file.Target, err)
		}
	}
	return nil
}
func (r Restart) Rollback() error {
	journal, err := r.journal()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for index := len(journal.Files) - 1; index >= 0; index-- {
		file := journal.Files[index]
		if file.Existed {
			var stage string
			stage, err = stagedCopy(file.Backup, file.Target, os.FileMode(file.Mode))
			if err == nil {
				err = os.Rename(stage, file.Target)
				if err != nil {
					os.Remove(stage)
				}
			}
		} else {
			err = os.Remove(file.Target)
			if errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		}
		if err != nil {
			return err
		}
		os.Remove(file.Staged)
	}
	return nil
}
