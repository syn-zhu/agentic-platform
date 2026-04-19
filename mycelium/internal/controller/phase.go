package controller

import (
	"context"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	capicond "sigs.k8s.io/cluster-api/util/conditions"
)

// stage is the fundamental unit of work in the reconcile event loop.
// It returns nil on success (condition written via capicond.Set) or a non-nil
// error on transient failure (condition must not be written; will retry).
type stage func(ctx context.Context) (*metav1.Condition, error)

// condGetter is the minimal interface runTasks needs from the reconciled object:
// current conditions and the generation they were observed at.
type condGetter interface {
	capicond.Getter
	capicond.Setter
	metav1.Object
}

// task[T] pairs a phase constructor with the condition prerequisites that must
// be True before the phase may run.
type task[T condGetter] struct {
	fn       func(helper, T) stage
	requires []string
}

// do builds a task[T], inferring T from fn. requires is variadic so callers
// omit the slice literal: do(phaseX, condA, condB).
func do[T condGetter](fn func(helper, T) stage, requires ...string) task[T] {
	return task[T]{fn: fn, requires: requires}
}

// runTasks compiles a task graph and returns a runner. Invoke the runner with
// ctx to execute: each round, every task whose required conditions are all
// True in proj.GetConditions() for the current generation runs. Rounds repeat
// until no eligible tasks remain or all tasks have run. base and proj are
// applied to each phase constructor at run time.
func runTasks[T condGetter](proj T, base helper) func(...task[T]) func(context.Context) error {
	return func(tasks ...task[T]) func(context.Context) error {
		return func(ctx context.Context) error {
			// TODO: should we be initializing list with condition state?
			// or, maybe we should compare our own last observed timestamp vs the actual
			// conditions' observed timestamps?
			// observed generation might not mean much if this is a reference
			//

			done := make([]bool, len(tasks))
			adj := make(map[string][]task[T])
			for {
				conds := proj.GetConditions()
				gen := proj.GetGeneration()
				var ready []int
				for i, t := range tasks {
					if !done[i] && conditionsAllTrue(conds, t.requires, gen) {
						ready = append(ready, i)
					}
				}
				if len(ready) == 0 {
					return nil
				}
				var errs []error
				for _, i := range ready {
					cond, err := tasks[i].fn(base, proj)(ctx)
					if cond != nil {
						capicond.Set(proj, *cond)
					}
					errs = append(errs, err)
					done[i] = true
				}
				if err := kerrors.NewAggregate(errs); err != nil {
					return err
				}
			}
		}
	}
}

// conditionsAllTrue reports whether every named condition is ConditionTrue for
// the given generation. A condition from a prior generation is treated as
// absent — it has not yet been re-evaluated for the current spec.
// An empty names slice is always satisfied.
func conditionsAllTrue(conds []metav1.Condition, names []string, generation int64) bool {
	for _, name := range names {
		// TODO: should we also check if last transition time is after the root object's last transition time?
		cond := apimeta.FindStatusCondition(conds, name)
		if cond == nil || cond.Status != metav1.ConditionTrue || cond.ObservedGeneration != generation {
			return false
		}
	}
	return true
}
