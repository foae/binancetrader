package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Config holds strategy parameters for the service.
type Config struct {
	Asset  string // Required: asset identifier (e.g., "btc", "eth")
	DryRun bool
}

// WindowPhase tracks progress through a single trading window.
type WindowPhase int

const (
	PhaseWaiting WindowPhase = iota
	PhaseDone
)

// WindowState holds all state for a single trading window.
type WindowState struct {
	Slug        string
	WindowStart time.Time
	WindowEnd   time.Time
	Phase       WindowPhase
}

// storageClient defines the subset of the storage layer used by the service.
type storageClient interface {
	Close() error
}

type Service struct {
	db        storageClient
	cfg       Config
	ctx       context.Context
	tradingCh chan *WindowState
	log       *slog.Logger // asset-scoped base logger
}

func New(ctx context.Context, db storageClient, cfg Config) (*Service, error) {
	if cfg.Asset == "" {
		return nil, fmt.Errorf("Asset must be set")
	}

	svc := &Service{
		db:        db,
		cfg:       cfg,
		ctx:       ctx,
		tradingCh: make(chan *WindowState, 2),
		log:       slog.With("asset", cfg.Asset),
	}

	go svc.discoveryLoop()
	go svc.tradingLoop()

	return svc, nil
}

// Close closes the service resources.
func (s *Service) Close() error {
	return nil
}

// currentWindowStart returns the most recent 15-minute boundary at or before t.
func currentWindowStart(t time.Time) time.Time {
	t = t.UTC()
	minute := t.Minute()
	aligned := minute - (minute % 15)
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), aligned, 0, 0, time.UTC)
}

// nextWindowStart returns the next 15-minute boundary strictly after t.
func nextWindowStart(t time.Time) time.Time {
	return currentWindowStart(t).Add(15 * time.Minute)
}

// windowSlug returns a unique identifier for a given asset and window start time.
func windowSlug(asset string, windowStart time.Time) string {
	return fmt.Sprintf("%s-%d", asset, windowStart.UTC().Unix())
}

// discoveryLoop periodically discovers new trading windows and sends them
// to tradingCh for processing. Ticks every 30s, fires immediately on start.
func (s *Service) discoveryLoop() {
	defer close(s.tradingCh)

	l := s.log.With("component", "discovery")

	if s.cfg.DryRun {
		l.Info("Strategy running in DRY RUN mode — no real orders will be placed")
	}

	l.Info("Discovery loop started")

	tracked := make(map[string]time.Time) // slug -> windowEnd

	// Check context before firing the immediate tick.
	select {
	case <-s.ctx.Done():
		return
	default:
	}

	// Fire immediately on start.
	s.discoveryTick(l, tracked)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.discoveryTick(l, tracked)
		}
	}
}

// discoveryTick runs a single tick of window discovery. It checks the current
// and next windows, and sends undiscovered ones to tradingCh.
func (s *Service) discoveryTick(l *slog.Logger, tracked map[string]time.Time) {
	now := time.Now().UTC()

	// Prune expired entries.
	for slug, windowEnd := range tracked {
		if now.After(windowEnd) {
			delete(tracked, slug)
		}
	}

	candidates := []time.Time{currentWindowStart(now), nextWindowStart(now)}

	for _, ws := range candidates {
		slug := windowSlug(s.cfg.Asset, ws)

		// Skip already-tracked windows.
		if _, ok := tracked[slug]; ok {
			continue
		}

		windowEnd := ws.Add(15 * time.Minute)

		// Skip windows that already ended.
		if now.After(windowEnd) {
			continue
		}

		tracked[slug] = windowEnd

		state := &WindowState{
			Slug:        slug,
			WindowStart: ws,
			WindowEnd:   windowEnd,
			Phase:       PhaseWaiting,
		}

		l.Info("Window discovered", "window", slug)

		// TODO: Add exchange-specific market discovery here.

		select {
		case s.tradingCh <- state:
		case <-s.ctx.Done():
			return
		}
	}
}

// tradingLoop reads discovered windows from tradingCh and processes each one.
func (s *Service) tradingLoop() {
	for state := range s.tradingCh {
		s.tradeWindow(state)
	}
}

// tradeWindow handles the full lifecycle of a single discovered window.
func (s *Service) tradeWindow(state *WindowState) {
	l := s.log.With("component", "trading", "window", state.Slug)
	l.Info("Processing window")

	// TODO: Implement strategy-specific trading logic here.
	// The pattern is:
	//   1. sleepUntil(checkpoint time)
	//   2. Evaluate market conditions
	//   3. Decide whether to enter
	//   4. Place order if criteria met

	state.Phase = PhaseDone
}

// sleepUntil blocks until the target time or context cancellation.
// Returns true if the target time was reached, false if context was cancelled.
func (s *Service) sleepUntil(t time.Time) bool {
	d := time.Until(t)
	if d <= 0 {
		return true
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-s.ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
