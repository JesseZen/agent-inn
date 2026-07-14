package manager

import "time"

func startHostedTurnWatcherLoop(interval time.Duration, beforePoll func() bool, poll func() error, onError func(error)) func() {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			default:
			}
			select {
			case <-ticker.C:
				if beforePoll != nil && !beforePoll() {
					return
				}
				if err := poll(); err != nil {
					onError(err)
				}
			case <-done:
				return
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}
