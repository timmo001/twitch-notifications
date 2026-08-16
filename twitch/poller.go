package twitch

import (
	"context"
	"log"
	"sync"
	"time"
)

// StreamOnlineCallback is a function called when a stream goes online
type StreamOnlineCallback func(event StreamOnlineEvent)

// Poller polls the Twitch API for live streams on channels that exceed the EventSub limit
type Poller struct {
	helixClient    *HelixClient
	channels       []Channel // Channels to poll (overflow channels beyond EventSub limit)
	pollInterval   time.Duration
	onStreamOnline StreamOnlineCallback
	liveStreams    map[string]bool // Track which channels are currently live
	liveStreamsMu  sync.RWMutex
	isFirstPoll    bool // Track if this is the first poll (to skip notifications for already-live streams)
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	started        bool
	startedMu      sync.Mutex
}

// NewPoller creates a new Poller for overflow channels
func NewPoller(helixClient *HelixClient, channels []Channel, pollInterval time.Duration, onStreamOnline StreamOnlineCallback) *Poller {
	return &Poller{
		helixClient:    helixClient,
		channels:       channels,
		pollInterval:   pollInterval,
		onStreamOnline: onStreamOnline,
		liveStreams:    make(map[string]bool),
		isFirstPoll:    true,
	}
}

// Start begins the polling loop in a background goroutine
func (p *Poller) Start(parentCtx context.Context) {
	p.startedMu.Lock()
	if p.started {
		p.startedMu.Unlock()
		return
	}
	p.started = true
	p.startedMu.Unlock()

	p.ctx, p.cancel = context.WithCancel(parentCtx)

	p.wg.Add(1)
	go p.pollLoop()

	log.Printf("Poller started for %d overflow channels (polling every %v)", len(p.channels), p.pollInterval)
}

// Stop stops the polling loop and waits for it to finish
func (p *Poller) Stop() {
	p.startedMu.Lock()
	if !p.started {
		p.startedMu.Unlock()
		return
	}
	p.startedMu.Unlock()

	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	log.Println("Poller stopped")
}

// GetPolledChannels returns the list of channels being polled
func (p *Poller) GetPolledChannels() []Channel {
	return p.channels
}

// GetPolledChannelIDs returns the IDs of channels being polled
func (p *Poller) GetPolledChannelIDs() []string {
	ids := make([]string, len(p.channels))
	for i, ch := range p.channels {
		ids[i] = ch.ID
	}
	return ids
}

// IsChannelLive returns whether a polled channel is currently live
func (p *Poller) IsChannelLive(channelID string) bool {
	p.liveStreamsMu.RLock()
	defer p.liveStreamsMu.RUnlock()
	return p.liveStreams[channelID]
}

// GetLiveChannelIDs returns the IDs of all currently live polled channels
func (p *Poller) GetLiveChannelIDs() []string {
	p.liveStreamsMu.RLock()
	defer p.liveStreamsMu.RUnlock()

	var live []string
	for id, isLive := range p.liveStreams {
		if isLive {
			live = append(live, id)
		}
	}
	return live
}

// pollLoop is the main polling loop
func (p *Poller) pollLoop() {
	defer p.wg.Done()

	// Do an immediate first poll
	p.poll()

	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.poll()
		}
	}
}

// poll checks for live streams and triggers callbacks for newly live channels
func (p *Poller) poll() {
	if len(p.channels) == 0 {
		return
	}

	channelIDs := p.GetPolledChannelIDs()

	// Debug: only log polling activity occasionally to reduce log spam
	// The important events (streams going live) are always logged

	liveStreams, err := p.helixClient.GetLiveStreams(p.ctx, channelIDs)
	if err != nil {
		log.Printf("Poller: Failed to check live streams: %v", err)
		return
	}

	// Build a set of currently live channel IDs
	currentlyLive := make(map[string]bool)
	for id := range liveStreams {
		currentlyLive[id] = true
	}

	// Check for newly live streams (was not live before, is live now)
	p.liveStreamsMu.Lock()
	isFirstPoll := p.isFirstPoll
	p.isFirstPoll = false

	newlyLive := make([]LiveStream, 0)
	for channelID, stream := range liveStreams {
		wasLive := p.liveStreams[channelID]
		if !wasLive {
			// Channel just went live
			newlyLive = append(newlyLive, stream)
		}
	}

	// Update our tracking map
	p.liveStreams = currentlyLive
	p.liveStreamsMu.Unlock()

	// Only log when there are live streams to reduce log noise
	if len(liveStreams) > 0 {
		log.Printf("Poller: %d/%d overflow channels are live", len(liveStreams), len(channelIDs))
	}

	// Send notifications for newly live streams
	// Skip notifications on first poll to avoid notifying for already-live streams at startup
	if isFirstPoll {
		if len(newlyLive) > 0 {
			log.Printf("Poller: Skipping notifications for %d already-live channels on first poll", len(newlyLive))
		}
		return
	}

	for _, stream := range newlyLive {
		log.Printf("Poller: Stream went live: %s (%s) - %s",
			stream.BroadcasterUserName, stream.BroadcasterUserLogin, stream.StreamTitle)

		// Convert LiveStream to StreamOnlineEvent for the callback
		event := StreamOnlineEvent{
			BroadcasterUserID:    stream.BroadcasterUserID,
			BroadcasterUserLogin: stream.BroadcasterUserLogin,
			BroadcasterUserName:  stream.BroadcasterUserName,
			StreamTitle:          stream.StreamTitle,
			GameName:             stream.GameName,
			StartedAt:            stream.StartedAt,
		}

		if p.onStreamOnline != nil {
			p.onStreamOnline(event)
		}
	}
}

// ForceCheck triggers an immediate poll check (useful for startup/manual recheck)
// Returns the currently live streams and updates internal live-state tracking.
// Accepts a context parameter to avoid issues with nil/cancelled internal context.
func (p *Poller) ForceCheck(ctx context.Context) (map[string]LiveStream, error) {
	if len(p.channels) == 0 {
		return make(map[string]LiveStream), nil
	}

	channelIDs := p.GetPolledChannelIDs()
	liveStreams, err := p.helixClient.GetLiveStreams(ctx, channelIDs)
	if err != nil {
		return nil, err
	}

	currentLive := make(map[string]bool, len(liveStreams))
	for id := range liveStreams {
		currentLive[id] = true
	}

	p.liveStreamsMu.Lock()
	p.liveStreams = currentLive
	p.isFirstPoll = false
	p.liveStreamsMu.Unlock()

	return liveStreams, nil
}
