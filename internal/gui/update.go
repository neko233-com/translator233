package gui

import (
	"context"
	"time"

	"github.com/neko233-com/translator233/internal/update"
)

// BindCloser lets the window quit itself once an update is being installed.
func (s *State) BindCloser(fn func()) {
	s.mu.Lock()
	s.closer = fn
	s.mu.Unlock()
}

// CheckUpdate looks for a newer release in the background.
func (s *State) CheckUpdate() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		rel, err := update.Latest(ctx)
		if err != nil {
			return
		}
		if !update.Available(s.Version, rel) {
			return
		}
		s.mu.Lock()
		s.UpdateTo = rel.Version()
		s.mu.Unlock()
		s.post(func() {})
	}()
}

// ApplyUpdate downloads and installs the release found by CheckUpdate.
func (s *State) ApplyUpdate() {
	s.mu.Lock()
	target := s.UpdateTo
	busy := s.Updating
	s.mu.Unlock()
	if target == "" || busy {
		return
	}
	s.mu.Lock()
	s.Updating = true
	s.Note = "正在下载 " + target + " …"
	s.mu.Unlock()
	s.post(func() {})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		rel, err := update.Latest(ctx)
		if err != nil {
			s.finishUpdate("检查更新失败: " + err.Error())
			return
		}
		plan, err := update.Prepare(s.Version, rel)
		if err != nil || plan == nil {
			s.finishUpdate("已是最新版本")
			return
		}
		path, err := plan.Download(ctx)
		if err != nil {
			s.finishUpdate("下载失败: " + err.Error())
			return
		}
		if want, ok := plan.Checksums(ctx); ok {
			if err := update.VerifySHA256(path, want); err != nil {
				s.finishUpdate(err.Error())
				return
			}
		}
		if err := update.Install(ctx, path, plan.Installer); err != nil {
			s.finishUpdate("安装失败: " + err.Error())
			return
		}
		s.mu.Lock()
		s.Updating = false
		s.Note = "正在完成更新…"
		closer := s.closer
		s.mu.Unlock()
		s.post(func() {})
		time.Sleep(300 * time.Millisecond)
		if closer != nil {
			closer()
		}
	}()
}

func (s *State) finishUpdate(msg string) {
	s.mu.Lock()
	s.Updating = false
	s.Note = msg
	s.mu.Unlock()
	s.post(func() {})
}
