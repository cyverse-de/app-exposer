package common

import "github.com/cyverse-de/messaging/v12"

// MapPodPhaseToStatus translates a Kubernetes pod phase into the corresponding
// JobState used by the job-status pipeline. Unknown phases fall back to
// SubmittedState so analyses that haven't reached a recognized phase aren't
// silently treated as Running or terminal.
func MapPodPhaseToStatus(phase string) messaging.JobState {
	switch phase {
	case "Pending":
		return messaging.SubmittedState
	case "Running":
		return messaging.RunningState
	case "Succeeded":
		return messaging.SucceededState
	case "Failed":
		return messaging.FailedState
	default:
		return messaging.SubmittedState
	}
}
