// Package installedinstance provides the small, Windows-only control plane
// used by an installed Dashboard executable.  It deliberately does not use
// HTTP for process discovery or lifecycle control: the endpoint is a named
// pipe scoped to the current user and executable identity.
package installedinstance

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrUnsupported is returned by installed-instance operations on non-Windows
	// systems.  The ordinary Dashboard command remains available there.
	ErrUnsupported = errors.New("installed instance mode is supported on Windows only")
	// ErrAlreadyRunning means another process owns this executable's instance
	// lease.  Callers may use Activate to ask that process to open its URL.
	ErrAlreadyRunning = errors.New("an installed Dashboard instance is already running")
	// ErrNoInstance means the scoped pipe is not present. Query treats this as
	// an ordinary stopped state and Stop treats it as successful idempotence.
	ErrNoInstance = errors.New("no installed Dashboard instance is running")
)

const (
	defaultRequestTimeout  = 2 * time.Second
	defaultStartupTimeout  = 8 * time.Second
	defaultShutdownTimeout = 15 * time.Second
)

// Config identifies one installed executable. ExecutablePath must be the
// executable that owns the instance (normally os.Executable()).
type Config struct {
	ExecutablePath  string
	RequestTimeout  time.Duration
	StartupTimeout  time.Duration
	ShutdownTimeout time.Duration
}

func (c Config) normalized() Config {
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = defaultRequestTimeout
	}
	if c.StartupTimeout <= 0 {
		c.StartupTimeout = defaultStartupTimeout
	}
	if c.ShutdownTimeout <= 0 {
		c.ShutdownTimeout = defaultShutdownTimeout
	}
	return c
}

// Status is the bounded machine-readable installed-instance status contract.
// URL is empty while the primary process is still initializing.
type Status struct {
	Running        bool   `json:"running"`
	PID            uint32 `json:"pid"`
	URL            string `json:"url"`
	InstanceID     string `json:"instanceId"`
	ExecutablePath string `json:"executablePath"`
}

// Callbacks are owned by cmd/dashboard. Activate runs only after the peer has
// been authenticated by the named-pipe server. Shutdown is expected to invoke
// the Dashboard's existing BeginShutdown -> HTTP Shutdown -> Manager Shutdown
// sequence; installedinstance never stops RoboRev itself.
type Callbacks struct {
	Activate func(Status)
	Shutdown func(context.Context) error
}

// Manager owns the exclusive process lease and the named-pipe server.
type Manager struct {
	platform managerPlatform
}

type managerPlatform interface {
	Run(context.Context) error
	SetStatus(Status)
	Status() Status
	Close() error
}

// Acquire claims the scoped installed-instance lease and creates its native
// IPC endpoint. It does not start serving requests until Run is called.
func Acquire(config Config, callbacks Callbacks) (*Manager, error) {
	config = config.normalized()
	p, err := acquirePlatform(config, callbacks)
	if err != nil {
		return nil, err
	}
	return &Manager{platform: p}, nil
}

// Run serves bounded status, activate, and shutdown requests until ctx ends.
func (m *Manager) Run(ctx context.Context) error {
	if m == nil || m.platform == nil {
		return ErrUnsupported
	}
	return m.platform.Run(ctx)
}

// SetStatus publishes the primary's URL and any other bounded state. Call it
// only after the HTTP listener has been successfully bound.
func (m *Manager) SetStatus(status Status) {
	if m != nil && m.platform != nil {
		m.platform.SetStatus(status)
	}
}

// Status returns a local snapshot of the published state.
func (m *Manager) Status() Status {
	if m == nil || m.platform == nil {
		return Status{}
	}
	return m.platform.Status()
}

// Close releases the native endpoint and process lease.
func (m *Manager) Close() error {
	if m == nil || m.platform == nil {
		return nil
	}
	return m.platform.Close()
}

// Query returns the scoped primary status. A missing primary is represented by
// Status{Running:false} with a nil error to make polling scripts deterministic.
func Query(ctx context.Context, config Config) (Status, error) {
	return queryPlatform(ctx, config.normalized())
}

// Activate asks the scoped primary to open its own URL. The caller never opens
// a URL directly, so duplicate invocations cannot create browser tabs that use
// a guessed localhost port.
func Activate(ctx context.Context, config Config) (Status, error) {
	return activatePlatform(ctx, config.normalized(), true)
}

// ActivateQuiet waits for the authenticated primary to become ready without
// asking it to open another browser tab. It is used by --installed --no-open
// duplicate invocations so they still wait through startup instead of
// returning merely because the process lease exists.
func ActivateQuiet(ctx context.Context, config Config) (Status, error) {
	return activatePlatform(ctx, config.normalized(), false)
}

// Stop asks the scoped primary to shut down, then waits for the exact PID that
// was authenticated by that primary to exit. Missing instances are success.
func Stop(ctx context.Context, config Config) error {
	return stopPlatform(ctx, config.normalized())
}
