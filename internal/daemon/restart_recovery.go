package daemon

import "context"

const daemonRestartReason = "daemon restarted without a running handle"

func (s *server) recoverRestartState() {
	if s.repo == nil {
		return
	}
	_, _ = s.repo.MarkInterruptedForegroundTasks(context.Background(), daemonRestartReason)
	_, _ = s.repo.MarkLostBackgroundTasks(context.Background(), daemonRestartReason)
	_, _ = s.repo.ExplainRestartState(context.Background(), daemonRestartReason)
}

func (s *server) startRecoveredQueues() {
	s.startNextForegroundQueued()
}
