package plan

import (
	"os"

	"github.com/danielriddell21/letsgo/internal/git"

	"github.com/danielriddell21/letsgo/internal/goproxy"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/gobuild"
)

// machineSettings is the machine's global config and the programs resolved
// from it, settled once before anything shells out so that every later step
// is handed them rather than re-reading the machine.
type machineSettings struct {
	global    *config.Global
	globalErr error

	goBin, goSource   string
	gitBin, gitSource string
	gitErr            error
}

// resolveMachine settles the machine's configuration: the global config
// (read here only when the caller supplied neither it nor the reason it could
// not) and the go and git commands it and the environment select.
func resolveMachine(global *config.Global, globalErr error) machineSettings {
	m := machineSettings{global: global, globalErr: globalErr}
	if m.global == nil {
		if m.globalErr == nil {
			m.global, m.globalErr = config.LoadGlobal()
		}
		if m.globalErr != nil {
			m.global = &config.Global{}
		}
	}
	if path, source, err := gobuild.Toolchain(m.global); err == nil {
		m.goBin, m.goSource = path, source
	}
	m.gitBin, m.gitSource, m.gitErr = git.Binary(m.global)
	return m
}

// recordMachine records the machine settings on the plan, and where each
// machine-level value it can influence — the go toolchain, git, and the
// module proxy — actually came from, so `plan --explain` can name the source
// of each (CD-10). A malformed global file is a Fail here rather than a
// silent fallback: every other package that consults the global config falls
// back to its defaults on error, trusting this check to surface the problem
// once instead of nowhere.
func (p *Plan) recordMachine(m machineSettings) {
	if m.globalErr != nil {
		p.add("global config", Fail, "%v", m.globalErr)
	}
	p.Global = m.global
	p.GoBin, p.GitBin = m.goBin, m.gitBin

	if m.goBin != "" {
		p.note("go", m.goBin, m.goSource)
	}
	if m.gitErr == nil {
		p.note("git", m.gitBin, m.gitSource)
	}
	proxy, source := goproxy.ResolveProxy(os.Getenv("GOPROXY"), p.Global.Proxy, p.Global.Path)
	p.Proxy = proxy
	p.note("proxy", proxy, source)
}
