// Package wfid owns the workflow-ID naming convention: the one place that knows
// how a workflow ID encodes what kind of work it is, and how a fleet node's child
// ID is composed from its parent's.
//
// The convention is load-bearing outside the workflows that mint it: the CLI's
// live `list` view labels rows by it, and the read side of the HTTP API
// reconstructs a fleet's node tree from it (a fleet node's child workflow ID is
// "<fleetID>-<nodeID>"). Both used to re-derive it from string prefixes locally,
// which is a rule two places had to keep agreeing on. Here it is a single tested
// function instead.
package wfid

import (
	"strings"

	"github.com/google/uuid"
)

// Class is what a workflow ID says the execution is. The values match the kinds
// the durable execution record uses (see execstore.Kind), so a row in `history`
// and a row in `list` describe the same thing by the same name; the classes that
// have no recorded kind of their own (a fleet node, an open-PR stage) are named
// after the step they are.
type Class string

const (
	// ClassRun is a PromptWorkflow execution: a `run`, a `template run` or a
	// schedule-fired run. It is also the fallback for an ID that matches no other
	// convention.
	ClassRun Class = "run"
	// ClassDevelop is a DevelopWorkflow execution started as `code develop`.
	ClassDevelop Class = "develop"
	// ClassReview is a ReviewWorkflow execution.
	ClassReview Class = "review"
	// ClassPilot is a PilotWorkflow execution.
	ClassPilot Class = "pilot"
	// ClassOpenPR is the open-PR stage of the develop pipeline.
	ClassOpenPR Class = "open-pr"
	// ClassFleet is a FleetWorkflow parent: the `fleet execute` orchestrator.
	ClassFleet Class = "fleet"
	// ClassFleetNode is a per-node child of a fleet parent, i.e. the develop
	// workflow one plan node runs in.
	ClassFleetNode Class = "fleet-node"
	// ClassFleetPlan is a FleetPlanWorkflow execution: the `fleet plan` agent run.
	ClassFleetPlan Class = "fleet-plan"
	// ClassSchedule is a schedule's own ID (schedules are not workflows, but the
	// live view lists them next to workflows).
	ClassSchedule Class = "schedule"
)

// Prefixes of the ID conventions, exported so a caller can build an ID with the
// same strings this package parses.
const (
	fleetPlanPrefix = "fleet-plan-"
	fleetPrefix     = "fleet-"
	developPrefix   = "develop-"
	reviewPrefix    = "review-"
	pilotPrefix     = "pilot-"
	openPRPrefix    = "open-pr-"
	schedulePrefix  = "schedule-"
)

// Classify labels a workflow ID by its convention.
//
// Fleet parents ("fleet-<uuid>") and their per-node develop children
// ("fleet-<uuid>-<nodeID>") share the "fleet-" prefix, so the two are told apart
// by whether the text after the prefix is a bare UUID (the parent) or a UUID with
// a "-<nodeID>" suffix (a child node).
func Classify(id string) Class {
	switch {
	case strings.HasPrefix(id, fleetPlanPrefix):
		return ClassFleetPlan
	case strings.HasPrefix(id, fleetPrefix):
		if _, err := uuid.Parse(strings.TrimPrefix(id, fleetPrefix)); err == nil {
			return ClassFleet
		}
		return ClassFleetNode
	case strings.HasPrefix(id, developPrefix):
		return ClassDevelop
	case strings.HasPrefix(id, reviewPrefix):
		return ClassReview
	case strings.HasPrefix(id, pilotPrefix):
		return ClassPilot
	case strings.HasPrefix(id, openPRPrefix):
		return ClassOpenPR
	case strings.HasPrefix(id, schedulePrefix):
		return ClassSchedule
	default:
		return ClassRun
	}
}

// FleetNodeWorkflowID returns the workflow ID a fleet's node runs its child
// develop workflow under. It mirrors how the fleet workflow composes the ID, so a
// reader can match a plan node to its execution without guessing.
func FleetNodeWorkflowID(fleetID, nodeID string) string {
	return fleetID + "-" + nodeID
}

// FleetNodeID returns the plan node a child workflow ID belongs to, and whether
// the ID really is a node of that fleet. It is the inverse of
// FleetNodeWorkflowID, so the reader that reconciles a plan against its child
// executions never has to slice strings itself.
func FleetNodeID(fleetID, workflowID string) (string, bool) {
	prefix := fleetID + "-"
	if fleetID == "" || !strings.HasPrefix(workflowID, prefix) {
		return "", false
	}
	nodeID := strings.TrimPrefix(workflowID, prefix)
	if nodeID == "" {
		return "", false
	}
	return nodeID, true
}
