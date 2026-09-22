package adapter

import (
	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/amurg-ai/amurg/runtime/internal/config"
	"reflect"
	"testing"
)

func TestSessionLaunchPersistsWithoutChangingOtherSessions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	for _, profile := range []string{"codex", "opencode"} {
		t.Run(profile, func(t *testing.T) {
			cfg := config.AgentConfig{Profile: profile, SessionKey: "runtime/user/" + profile, Security: &config.SecurityConfig{Cwd: "/original"}, Codex: &config.CodexConfig{Args: []string{"--model", "example"}}, OpenCode: &config.OpenCodeConfig{Args: []string{"--agent", "build"}}}
			before := *cfg.Security
			launch := &protocol.SessionLaunch{WorkDir: dir, Yolo: true, Args: []string{"literal argument; $(never-execute)"}}
			updated, err := WithSessionLaunch(cfg, launch)
			if err != nil {
				t.Fatal(err)
			}
			native, err := terminalLaunchConfig(updated)
			if err != nil {
				t.Fatal(err)
			}
			if native.WorkDir != dir {
				t.Fatalf("cwd: %s", native.WorkDir)
			}
			flag := "--auto"
			if profile == "codex" {
				flag = "--dangerously-bypass-approvals-and-sandbox"
			}
			if native.Args[len(native.Args)-1] != flag {
				t.Fatalf("permission flags: %v", native.Args)
			}
			restored, err := WithSessionLaunch(cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(updated, restored) {
				t.Fatal("launch options not restored after restart")
			}
			if !reflect.DeepEqual(before, *cfg.Security) || len(cfg.Codex.Args) != 2 || len(cfg.OpenCode.Args) != 2 {
				t.Fatal("base agent config mutated")
			}
			other := cfg
			other.SessionKey += "-other"
			untouched, err := WithSessionLaunch(other, nil)
			if err != nil || !reflect.DeepEqual(untouched, other) {
				t.Fatal("launch options leaked to another session")
			}
		})
	}
}
