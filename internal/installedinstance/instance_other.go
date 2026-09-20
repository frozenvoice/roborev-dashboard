//go:build !windows

package installedinstance

import "context"

type unsupportedPlatform struct{}

func acquirePlatform(Config, Callbacks) (managerPlatform, error) {
	return nil, ErrUnsupported
}

func queryPlatform(context.Context, Config) (Status, error) {
	return Status{}, ErrUnsupported
}

func activatePlatform(context.Context, Config, bool) (Status, error) {
	return Status{}, ErrUnsupported
}

func stopPlatform(context.Context, Config) error {
	return ErrUnsupported
}
