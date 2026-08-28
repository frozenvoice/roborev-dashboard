package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/froze/roborev-dashboard/internal/app"
	"github.com/froze/roborev-dashboard/internal/buildinfo"
	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
	"github.com/froze/roborev-dashboard/internal/setup"
)

func listenLocal(host string, port int) (net.Listener, error) {
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("bind address must be loopback")
	}
	for candidate := port; candidate < port+20; candidate++ {
		listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(candidate)))
		if err == nil {
			return listener, nil
		}
	}
	return nil, fmt.Errorf("no available local port in range %d-%d", port, port+19)
}

func openBrowser(address string) {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", address)
	case "darwin":
		command = exec.Command("open", address)
	default:
		command = exec.Command("xdg-open", address)
	}
	_ = command.Start()
}

func defaultConfigFile(override string) string {
	if override != "" {
		return filepath.Join(override, "config.json")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "roborev-dashboard", "config.json")
}

type startupRegistry interface {
	Load(context.Context)
	Add(context.Context, string) (gitx.Repository, error)
}

func initializeRegistry(registry startupRegistry, initialRepo string, timeout time.Duration) error {
	loadCtx, loadCancel := context.WithTimeout(context.Background(), timeout)
	registry.Load(loadCtx)
	loadCancel()

	if initialRepo == "" {
		return nil
	}
	addCtx, addCancel := context.WithTimeout(context.Background(), timeout)
	_, err := registry.Add(addCtx, initialRepo)
	addCancel()
	return err
}

func writeVersion(w io.Writer, jsonOutput bool) error {
	metadata, err := buildinfo.Current()
	if err != nil {
		return err
	}
	if jsonOutput {
		encoded, err := metadata.JSON()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(encoded))
		return err
	}
	_, err = io.WriteString(w, metadata.Human())
	return err
}

func main() {
	bind := flag.String("bind", "127.0.0.1", "loopback bind address")
	port := flag.Int("port", 7410, "starting local port")
	noOpen := flag.Bool("no-open", false, "do not open the default browser")
	repo := flag.String("repo", "", "Git repository to register and select")
	binaryOverride := flag.String("roborev", "", "explicit roborev executable path")
	daemonAddress := flag.String("roborev-server", "127.0.0.1:7373", "RoboRev daemon loopback address")
	configDir := flag.String("config-dir", "", "override Dashboard config directory")
	versionFlag := flag.Bool("version", false, "print Dashboard version")
	versionJSONFlag := flag.Bool("version-json", false, "print Dashboard version as JSON")
	flag.Parse()
	if *versionFlag || *versionJSONFlag {
		if err := writeVersion(os.Stdout, *versionJSONFlag); err != nil {
			log.Fatal(err)
		}
		return
	}

	listener, err := listenLocal(*bind, *port)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	runner := proc.ExecRunner{}
	gitService := gitx.New(runner)
	registry := app.NewRegistry(gitService, defaultConfigFile(*configDir))
	initialRepo := *repo
	if initialRepo == "" {
		initialRepo, _ = os.Getwd()
	}
	if err := initializeRegistry(registry, initialRepo, 10*time.Second); err != nil {
		log.Printf("Repository not registered: %v", err)
	}

	binary, binaryErr := roborev.ResolveBinary(*binaryOverride)
	if binaryErr != nil {
		log.Printf("RoboRev setup required: %v", binaryErr)
	}
	client, err := roborev.NewClientWithOptions(binary, runner, *daemonAddress, strings.TrimSpace(*binaryOverride) != "")
	if err != nil {
		log.Fatal(err)
	}
	installer := &roborevcompat.Installer{Downloader: roborevcompat.Downloader{}, Manifest: roborevcompat.Manifest064()}
	setupManager, err := setup.NewManager(client, installer)
	if err != nil {
		log.Fatal(err)
	}

	host := listener.Addr().String()
	serverHandler, err := app.NewServerWithSetup(client, registry, host, operation.NewGate(), setupManager)
	if err != nil {
		log.Fatal(err)
	}
	httpServer := &http.Server{Handler: serverHandler.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	url := "http://" + host
	fmt.Printf("RoboRev Dashboard: %s\n", url)
	if !*noOpen {
		go openBrowser(url)
	}

	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, shutdownSignals()...)
	<-stop
	streamShutdown, streamCancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = serverHandler.BeginShutdown(streamShutdown)
	streamCancel()
	httpShutdown, httpCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = httpServer.Shutdown(httpShutdown)
	httpCancel()
	refineShutdown, refineCancel := context.WithTimeout(context.Background(), 15*time.Second)
	_ = serverHandler.Shutdown(refineShutdown)
	refineCancel()
}
