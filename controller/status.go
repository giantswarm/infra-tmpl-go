package controller

import (
	"strings"

	"github.com/giantswarm/inago/common"
	"github.com/giantswarm/inago/fleet"
)

// UnitStatusList represents a list of UnitStatus.
type UnitStatusList []fleet.UnitStatus

// Group returns a shortened version of usl where equal status
// are replaced by one UnitStatus where the Name is replaced with "*".
func (usl UnitStatusList) Group() ([]fleet.UnitStatus, error) {
	matchers := map[string]struct{}{}
	newList := []fleet.UnitStatus{}

	for _, us := range usl {
		// Group unit status
		grouped, suffix, err := groupUnitStatus(usl, us)
		if err != nil {
			return nil, maskAny(err)
		}

		// Prevent doubled aggregation.
		if _, ok := matchers[suffix]; ok {
			continue
		}
		matchers[suffix] = struct{}{}

		// Aggregate.
		if allStatesEqual(grouped) {
			newStatus := grouped[0]
			newStatus.Name = "*"
			newList = append(newList, newStatus)
		} else {
			newList = append(newList, grouped...)
		}
	}

	return newList, nil
}

func (usl UnitStatusList) unitStatusBySliceID(sliceID string) (fleet.UnitStatus, error) {
	for _, us := range usl {
		ID, err := common.SliceID(us.Name)
		if err != nil {
			return fleet.UnitStatus{}, maskAny(err)
		}
		if ID == sliceID {
			// We are not interested in this slice during this iteration.
			return us, nil
		}
	}

	return fleet.UnitStatus{}, maskAny(unitSliceNotFoundError)
}

// groupUnitStatus returns a subset of usl where the sliceID matches the sliceID
// of groupMember, ignoring the unit names and extension.
func groupUnitStatus(usl []fleet.UnitStatus, groupMember fleet.UnitStatus) ([]fleet.UnitStatus, string, error) {
	ID, err := common.SliceID(groupMember.Name)
	if err != nil {
		return nil, "", maskAny(invalidUnitStatusError)
	}

	newList := []fleet.UnitStatus{}
	for _, us := range usl {
		exp := common.ExtExp.ReplaceAllString(us.Name, "")
		if !strings.HasSuffix(exp, ID) {
			continue
		}

		newList = append(newList, us)
	}

	return newList, ID, nil
}

// allStatesEqual returns true if all elements in usl match for the following
// fields: Current, Desired, Machine.SystemdActive, Machine.UnitHash
func allStatesEqual(usl []fleet.UnitStatus) bool {
	for _, us1 := range usl {
		for _, us2 := range usl {
			if us1.Current != us2.Current {
				return false
			}
			if us1.Desired != us2.Desired {
				return false
			}
			for _, m1 := range us1.Machine {
				for _, m2 := range us2.Machine {
					if m1.SystemdActive != m2.SystemdActive {
						return false
					}
					if m1.UnitHash != m2.UnitHash {
						return false
					}
				}
			}
		}
	}

	return true
}

func unitHasStatus(us fleet.UnitStatus, status Status) (bool, error) {
	for _, ms := range us.Machine {
		aggregated, err := AggregateStatus(us.Current, us.Desired, ms.SystemdActive, ms.SystemdSub)
		if err != nil {
			return false, maskAny(err)
		}

		if aggregated != StatusRunning {
			return false, nil
		}
	}

	return true, nil
}

type Status string

var (
	StatusFailed   Status = "failed"
	StatusNotFound Status = "not-found"
	StatusRunning  Status = "running"
	StatusStarting Status = "starting"
	StatusStopped  Status = "stopped"
	StatusStopping Status = "stopping"
)

type StatusContext struct {
	FleetCurrent  string
	FleetDesired  string
	SystemdActive string
	SystemdSub    string
	Aggregated    Status
}

var (
	StatusIndex = []StatusContext{
		{
			FleetCurrent:  "inactive",
			FleetDesired:  "*",
			SystemdActive: "*",
			SystemdSub:    "*",
			Aggregated:    StatusStopped,
		},
		{
			FleetCurrent:  "loaded|launched",
			FleetDesired:  "*",
			SystemdActive: "inactive",
			SystemdSub:    "*",
			Aggregated:    StatusStopped,
		},
		{
			FleetCurrent:  "loaded|launched",
			FleetDesired:  "*",
			SystemdActive: "failed",
			SystemdSub:    "*",
			Aggregated:    StatusFailed,
		},
		{
			FleetCurrent:  "loaded|launched",
			FleetDesired:  "*",
			SystemdActive: "activating",
			SystemdSub:    "*",
			Aggregated:    StatusStarting,
		},
		{
			FleetCurrent:  "loaded|launched",
			FleetDesired:  "*",
			SystemdActive: "deactivating",
			SystemdSub:    "*",
			Aggregated:    StatusStopping,
		},
		{
			FleetCurrent:  "loaded|launched",
			FleetDesired:  "*",
			SystemdActive: "active|reloading",
			SystemdSub:    "stop-sigterm|stop-post|stop",
			Aggregated:    StatusStopping,
		},
		{
			FleetCurrent:  "loaded|launched",
			FleetDesired:  "*",
			SystemdActive: "active|reloading",
			SystemdSub:    "auto-restart|launched|start-pre|start-pre|start-post|start|dead",
			Aggregated:    StatusStarting,
		},
		{
			FleetCurrent:  "loaded|launched",
			FleetDesired:  "*",
			SystemdActive: "active|reloading",
			SystemdSub:    "exited|running",
			Aggregated:    StatusRunning,
		},
	}
)

// AggregateStatus aggregates the given fleet and systemd states to a Status
// known to Inago based on the StatusIndex.
//
//   fc: fleet current state
//   fd: fleet desired state
//   sa: systemd active state
//   ss: systemd sub state
//
func AggregateStatus(fc, fd, sa, ss string) (Status, error) {
	for _, statusContext := range StatusIndex {
		if !matchState(statusContext.FleetCurrent, fc) {
			continue
		}
		if !matchState(statusContext.FleetDesired, fd) {
			continue
		}
		if !matchState(statusContext.SystemdActive, sa) {
			continue
		}
		if !matchState(statusContext.SystemdSub, ss) {
			continue
		}

		// All requirements matched, so return the aggregated status.
		return statusContext.Aggregated, nil
	}

	return "", maskAnyf(invalidUnitStatusError, "fc: %s, fd: %s, sa: %s, ss: %s", fc, fd, sa, ss)
}

func matchState(indexed, remote string) bool {
	if indexed == "*" {
		// When the indexed state is "*", we accept all states.
		return true
	}

	for _, splitted := range strings.Split(indexed, "|") {
		if splitted == remote {
			// When the indexed state is equal to the remote state, we accept it.
			return true
		}
	}

	// The given remote state does not match the criteria of the indexed state.
	return false
}
