package test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/memory-co/agentuse/server/internal/service/file"
	skillservice "github.com/memory-co/agentuse/server/internal/service/skill"
)

func skillZIP(t *testing.T, name, body string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, _ := w.Create("SKILL.md")
	fmt.Fprintf(f, "---\nname: %s\ndescription: Test skill\n---\n%s", name, body)
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}

func TestFailedSkillPublicationRemovesCandidateObject(t *testing.T) {
	s, u, _, _ := runStore(t)
	ctx := context.Background()
	files, e := file.NewLocal(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	s.ConfigureFiles(files)
	if _, e = s.DB.Exec(`CREATE TRIGGER fail_skill BEFORE INSERT ON skill_versions BEGIN SELECT RAISE(ABORT,'forced publication failure'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Skills.Publish(ctx, u, "", "", bytes.NewReader(skillZIP(t, "test", "body"))); e == nil {
		t.Fatal("database failure ignored")
	}
	rows, e := files.List(ctx, "skills", "")
	if e != nil || len(rows) != 0 {
		t.Fatal("unpublished package leaked", rows, e)
	}
	var count int
	if e = s.DB.QueryRow(`SELECT count(*) FROM skills`).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
}

func TestConcurrentSkillPublicationHasOneWinner(t *testing.T) {
	s, u, _, _ := runStore(t)
	ctx := context.Background()
	files, e := file.NewLocal(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	s.ConfigureFiles(files)
	first, e := s.Skills.Publish(ctx, u, "", "", bytes.NewReader(skillZIP(t, "review", "v1")))
	if e != nil {
		t.Fatal(e)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, body := range []string{"v2a", "v2b"} {
		data := skillZIP(t, "review", body)
		go func() {
			<-start
			_, err := s.Skills.Publish(ctx, u, first.ID, first.CurrentVersionID, bytes.NewReader(data))
			results <- err
		}()
	}
	close(start)
	wins, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		e := <-results
		if e == nil {
			wins++
		} else if errors.Is(e, skillservice.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal(wins, conflicts)
	}
	rows, e := files.List(ctx, "skills", first.ID)
	if e != nil || len(rows) != 2 {
		t.Fatal("losing upload leaked", rows, e)
	}
	history, e := s.Skills.Versions(ctx, u, first.ID)
	if e != nil || len(history) != 2 || history[0].Version != 2 {
		t.Fatal(history, e)
	}
}
