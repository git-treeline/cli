package cmd

import (
	"os"
	"path/filepath"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/proxy"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/service"
)

// wtAlloc is the current directory's registry allocation, resolved once so
// commands don't each repeat the cwd -> registry -> allocation lookup.
type wtAlloc struct {
	Path  string // absolute cwd
	Reg   *registry.Registry
	Entry registry.Allocation
}

// currentAllocation resolves the allocation for the current working
// directory. Returns errNoAllocation if nothing is registered for it.
func currentAllocation() (*wtAlloc, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	absPath, _ := filepath.Abs(cwd)

	reg := registry.New("")
	entry := reg.Find(absPath)
	if entry == nil {
		return nil, errNoAllocation(absPath)
	}

	return &wtAlloc{Path: absPath, Reg: reg, Entry: entry}, nil
}

// Ports returns the allocation's ports, or nil if none are assigned.
func (w *wtAlloc) Ports() []int {
	return format.GetPorts(format.Allocation(w.Entry))
}

// PrimaryPort returns the allocation's first port, or errNoAllocationNoPorts
// if it has none.
func (w *wtAlloc) PrimaryPort() (int, error) {
	ports := w.Ports()
	if len(ports) == 0 {
		return 0, errNoAllocationNoPorts(w.Path)
	}
	return ports[0], nil
}

// routerURLFor builds a router URL from the service-probe plumbing (router
// domain/port and the live socket / port-forward checks) shared by call
// sites that resolve a URL for a project/branch/port from user config.
func routerURLFor(uc *config.UserConfig, port int, project, branch string) string {
	return proxy.BuildRouterURL(port, project, branch, uc.RouterDomain(), uc.RouterPort(), service.IsRunning(), service.IsPortForwardConfigured())
}
