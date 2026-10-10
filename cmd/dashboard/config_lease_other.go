//go:build !windows

package main

import "io"

type configLease struct{}

func (configLease) Close() error { return nil }

func acquireConfigLease(string) (io.Closer, error) { return configLease{}, nil }
