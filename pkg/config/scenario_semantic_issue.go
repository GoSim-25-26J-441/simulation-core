package config

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	reWorkloadMissingEndpoint = regexp.MustCompile(`^workload (\d+): target endpoint ([^:]+):(\S+) does not exist$`)
	reWorkloadMissingService  = regexp.MustCompile(`^workload (\d+): target service (\S+) does not exist$`)
	reWorkloadInvalidToQuoted = regexp.MustCompile(`^workload (\d+): invalid to "([^"]*)": (.+)$`)
	reWorkloadInvalidTo       = regexp.MustCompile(`^workload (\d+): invalid to (.+): `)
	reDownstreamMissingEP     = regexp.MustCompile(`^service ([^,]+), endpoint (.+): downstream endpoint ([^:]+):(\S+) does not exist$`)
	reDownstreamMissingSvc    = regexp.MustCompile(`^service ([^,]+), endpoint (.+): downstream service (\S+) does not exist$`)
	reQueueConsumerEP         = regexp.MustCompile(`^service ([^:]+): queue consumer_target endpoint ([^:]+):(\S+) does not exist$`)
	reQueueConsumerSvc        = regexp.MustCompile(`^service ([^:]+): queue consumer_target service (\S+) does not exist$`)
	reQueueDLQEP              = regexp.MustCompile(`^service ([^:]+): queue dlq endpoint ([^:]+):(\S+) does not exist$`)
	reQueueDLQSvc             = regexp.MustCompile(`^service ([^:]+): queue dlq service (\S+) does not exist$`)
	reTopicConsumerEP         = regexp.MustCompile(`^service ([^:]+): topic subscriber consumer_target endpoint ([^:]+):(\S+) does not exist$`)
	reTopicConsumerSvc        = regexp.MustCompile(`^service ([^:]+): topic subscriber consumer_target service (\S+) does not exist$`)
	reTopicConsumerTargetErr  = regexp.MustCompile(`^service ([^:]+): behavior\.topic\.subscribers\[(\d+)\]\.consumer_target: `)
	reTopicConsumerRequired   = regexp.MustCompile(`^service ([^:]+): behavior\.topic\.subscribers\[(\d+)\]\.consumer_target is required$`)
	reQueueConsumerTargetErr  = regexp.MustCompile(`^service ([^:]+): behavior\.queue\.consumer_target: `)
)

// SemanticIssueFromValidateError maps [ValidateScenario] errors into a stable code,
// optional field path (for UI / editors), and a concise message.
func SemanticIssueFromValidateError(err error) (code, path, message string) {
	if err == nil {
		return "", "", ""
	}
	raw := err.Error()
	raw = strings.TrimPrefix(raw, "invalid scenario: ")
	message = raw

	if m := reWorkloadMissingEndpoint.FindStringSubmatch(raw); len(m) == 4 {
		svc, ep := m[2], m[3]
		return "UNKNOWN_WORKLOAD_ENDPOINT",
			fmt.Sprintf("workload[%s].to", m[1]),
			fmt.Sprintf("workload target %s:%s references missing endpoint %s on service %s", svc, ep, ep, svc)
	}
	if m := reWorkloadMissingService.FindStringSubmatch(raw); len(m) == 3 {
		return "UNKNOWN_WORKLOAD_SERVICE",
			fmt.Sprintf("workload[%s].to", m[1]),
			fmt.Sprintf("workload target references unknown service %s", m[2])
	}
	if m := reWorkloadInvalidToQuoted.FindStringSubmatch(raw); len(m) == 4 {
		return "INVALID_SCENARIO_SCHEMA", fmt.Sprintf("workload[%s].to", m[1]), raw
	}
	if m := reWorkloadInvalidTo.FindStringSubmatch(raw); len(m) == 3 {
		return "INVALID_SCENARIO_SCHEMA", fmt.Sprintf("workload[%s].to", m[1]), raw
	}

	if m := reDownstreamMissingEP.FindStringSubmatch(raw); len(m) == 5 {
		return "UNKNOWN_DOWNSTREAM_ENDPOINT",
			fmt.Sprintf(`services["%s"].endpoints["%s"].downstream`, m[1], m[2]),
			fmt.Sprintf("downstream target references missing endpoint %s on service %s", m[4], m[3])
	}
	if m := reDownstreamMissingSvc.FindStringSubmatch(raw); len(m) == 4 {
		return "UNKNOWN_DOWNSTREAM_SERVICE",
			fmt.Sprintf(`services["%s"].endpoints["%s"].downstream`, m[1], m[2]),
			fmt.Sprintf("downstream target references unknown service %s", m[3])
	}

	if m := reQueueConsumerEP.FindStringSubmatch(raw); len(m) == 4 {
		return "UNKNOWN_QUEUE_CONSUMER_TARGET",
			fmt.Sprintf(`services["%s"].behavior.queue.consumer_target`, m[1]),
			fmt.Sprintf("queue consumer_target references missing endpoint %s on service %s", m[3], m[2])
	}
	if m := reQueueConsumerSvc.FindStringSubmatch(raw); len(m) == 3 {
		return "UNKNOWN_QUEUE_CONSUMER_TARGET",
			fmt.Sprintf(`services["%s"].behavior.queue.consumer_target`, m[1]),
			fmt.Sprintf("queue consumer_target references unknown service %s", m[2])
	}

	if m := reQueueDLQEP.FindStringSubmatch(raw); len(m) == 4 {
		return "UNKNOWN_QUEUE_CONSUMER_TARGET",
			fmt.Sprintf(`services["%s"].behavior.queue.dlq_target`, m[1]),
			fmt.Sprintf("queue dlq_target references missing endpoint %s on service %s", m[3], m[2])
	}
	if m := reQueueDLQSvc.FindStringSubmatch(raw); len(m) == 3 {
		return "UNKNOWN_QUEUE_CONSUMER_TARGET",
			fmt.Sprintf(`services["%s"].behavior.queue.dlq_target`, m[1]),
			fmt.Sprintf("queue dlq_target references unknown service %s", m[2])
	}

	if m := reTopicConsumerEP.FindStringSubmatch(raw); len(m) == 4 {
		return "UNKNOWN_QUEUE_CONSUMER_TARGET",
			fmt.Sprintf(`services["%s"].behavior.topic.subscribers.consumer_target`, m[1]),
			fmt.Sprintf("topic subscriber consumer_target references missing endpoint %s on service %s", m[3], m[2])
	}
	if m := reTopicConsumerSvc.FindStringSubmatch(raw); len(m) == 3 {
		return "UNKNOWN_QUEUE_CONSUMER_TARGET",
			fmt.Sprintf(`services["%s"].behavior.topic.subscribers.consumer_target`, m[1]),
			fmt.Sprintf("topic subscriber consumer_target references unknown service %s", m[2])
	}
	if m := reTopicConsumerTargetErr.FindStringSubmatch(raw); len(m) == 3 {
		return "INVALID_SCENARIO_SCHEMA",
			fmt.Sprintf(`services["%s"].behavior.topic.subscribers[%s].consumer_target`, m[1], m[2]),
			raw
	}
	if m := reTopicConsumerRequired.FindStringSubmatch(raw); len(m) == 3 {
		return "INVALID_SCENARIO_SCHEMA",
			fmt.Sprintf(`services["%s"].behavior.topic.subscribers[%s].consumer_target`, m[1], m[2]),
			raw
	}

	if m := reQueueConsumerTargetErr.FindStringSubmatch(raw); len(m) == 2 {
		return "INVALID_SCENARIO_SCHEMA",
			fmt.Sprintf(`services["%s"].behavior.queue.consumer_target`, m[1]),
			raw
	}

	switch {
	case strings.Contains(raw, "duplicate host id:"):
		return "INVALID_SCENARIO_SCHEMA", "hosts", raw
	case strings.Contains(raw, "duplicate service id:"):
		return "INVALID_SCENARIO_SCHEMA", "services", raw
	case strings.HasPrefix(raw, "at least one host"):
		return "INVALID_SCENARIO_SCHEMA", "hosts", raw
	case strings.HasPrefix(raw, "at least one service"):
		return "INVALID_SCENARIO_SCHEMA", "services", raw
	case strings.HasPrefix(raw, "at least one workload"):
		return "INVALID_SCENARIO_SCHEMA", "workload", raw
	}

	return "INVALID_SCENARIO_SCHEMA", "", raw
}
