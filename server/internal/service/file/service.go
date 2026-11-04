// Package file stores bytes in isolated business namespaces, without database metadata.
package file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/google/uuid"
)

var ErrExists = errors.New("file already exists")

type Entry struct {
	Path string `json:"path"`
	Size int64  `json:"size_bytes"`
}
type Backend interface {
	Put(context.Context, string, io.ReadSeeker, int64, bool) error
	Get(context.Context, string) (io.ReadCloser, int64, error)
	List(context.Context, string) ([]Entry, error)
	Delete(context.Context, string) error
}
type Service struct{ backend Backend }

func New(backend Backend) *Service { return &Service{backend: backend} }
func Key(namespace, name string) (string, error) {
	if namespace == "" || strings.ContainsAny(namespace, "/\\\x00") || namespace == "." || namespace == ".." {
		return "", fmt.Errorf("invalid file namespace")
	}
	if name == "" || name == "." || strings.ContainsAny(name, "\\\x00") || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") {
		return "", fmt.Errorf("invalid relative file path")
	}
	return namespace + "/" + name, nil
}
func (s *Service) Put(ctx context.Context, namespace, name string, source io.Reader, exclusive bool) error {
	key, err := Key(namespace, name)
	if err != nil {
		return err
	}
	// A seekable spool supports signed S3 requests and prevents partial local publication.
	file, err := os.CreateTemp("", "agentuse-file-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	size, err := io.Copy(file, source)
	if err != nil {
		return err
	}
	if _, err = file.Seek(0, 0); err != nil {
		return err
	}
	return s.backend.Put(ctx, key, file, size, exclusive)
}
func (s *Service) Get(ctx context.Context, namespace, name string) (io.ReadCloser, int64, error) {
	key, err := Key(namespace, name)
	if err != nil {
		return nil, 0, err
	}
	return s.backend.Get(ctx, key)
}
func (s *Service) Delete(ctx context.Context, namespace, name string) error {
	key, err := Key(namespace, name)
	if err != nil {
		return err
	}
	return s.backend.Delete(ctx, key)
}
func (s *Service) List(ctx context.Context, namespace, prefix string) ([]Entry, error) {
	name := strings.TrimSuffix(prefix, "/")
	key, err := Key(namespace, name)
	if name == "" {
		key, err = Key(namespace, "_")
		key = strings.TrimSuffix(key, "/_")
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.backend.List(ctx, key+"/")
	for i := range rows {
		rows[i].Path = strings.TrimPrefix(rows[i].Path, namespace+"/")
	}
	return rows, err
}
func (s *Service) DeletePrefix(ctx context.Context, namespace, prefix string) error {
	rows, err := s.List(ctx, namespace, prefix)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err = s.Delete(ctx, namespace, row.Path); err != nil {
			return err
		}
	}
	return nil
}

type Local struct{ Root string }

func NewLocal(root string) (*Service, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	return New(Local{root}), nil
}
func (l Local) open(key string) (*os.Root, string, error) {
	base, err := os.OpenRoot(l.Root)
	if err != nil {
		return nil, "", err
	}
	defer base.Close()
	namespace, name, _ := strings.Cut(key, "/")
	if err = base.Mkdir(namespace, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, "", err
	}
	root, err := base.OpenRoot(namespace)
	return root, name, err
}
func (l Local) Put(ctx context.Context, key string, source io.ReadSeeker, size int64, exclusive bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, name, err := l.open(key)
	if err != nil {
		return err
	}
	defer root.Close()
	parent := path.Dir(name)
	if parent != "." {
		parts := strings.Split(parent, "/")
		for i := range parts {
			if err = root.Mkdir(strings.Join(parts[:i+1], "/"), 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
		}
	}
	temp := path.Join(parent, ".upload-"+uuid.NewString())
	out, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	_, err = io.Copy(out, source)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if exclusive {
		err = root.Link(temp, name)
		if errors.Is(err, os.ErrExist) {
			return ErrExists
		}
	} else {
		err = root.Rename(temp, name)
	}
	if err != nil {
		return err
	}
	dir, err := root.Open(parent)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (l Local) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	root, name, err := l.open(key)
	if err != nil {
		return nil, 0, err
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		if err == nil {
			err = fmt.Errorf("not a regular file")
		}
		return nil, 0, err
	}
	return f, info.Size(), nil
}
func (l Local) Delete(ctx context.Context, key string) error {
	root, name, err := l.open(key)
	if err != nil {
		return err
	}
	defer root.Close()
	err = root.Remove(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func (l Local) List(ctx context.Context, prefix string) ([]Entry, error) {
	root, name, err := l.open(strings.TrimSuffix(prefix, "/"))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if name == "" {
		name = "."
	}
	out := []Entry{}
	var walk func(string) error
	walk = func(dir string) error {
		f, e := root.Open(dir)
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		if e != nil {
			return e
		}
		rows, e := f.ReadDir(-1)
		f.Close()
		if e != nil {
			return e
		}
		for _, row := range rows {
			if e = ctx.Err(); e != nil {
				return e
			}
			p := path.Join(dir, row.Name())
			if row.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink in file space")
			}
			if row.IsDir() {
				if e = walk(p); e != nil {
					return e
				}
			} else {
				info, e := root.Lstat(p)
				if e != nil {
					return e
				}
				if info.Mode().IsRegular() && !strings.HasPrefix(row.Name(), ".upload-") {
					ns, _, _ := strings.Cut(prefix, "/")
					out = append(out, Entry{ns + "/" + p, info.Size()})
				}
			}
		}
		return nil
	}
	err = walk(name)
	return out, err
}
