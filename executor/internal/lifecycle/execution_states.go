package lifecycle

// ExecState represents the state of a single execution invocation.
type ExecState string

const (
	ExecPending       ExecState = "Pending"
	ExecActive        ExecState = "Active"
	ExecCompleted     ExecState = "Completed"
	ExecFailed        ExecState = "Failed"
	ExecInputRequired ExecState = "InputRequired"
	ExecCancelled     ExecState = "Cancelled"
)

// ExecTrigger represents triggers that cause execution state transitions.
type ExecTrigger string

const (
	ExecTrigRunReceived   ExecTrigger = "RunReceived"
	ExecTrigToolCallOut   ExecTrigger = "ToolCallOut"
	ExecTrigToolCallIn    ExecTrigger = "ToolCallIn"
	ExecTrigToolCallError ExecTrigger = "ToolCallError"
	ExecTrigReentrantIn   ExecTrigger = "ReentrantIn"
	ExecTrigReentrantOut  ExecTrigger = "ReentrantOut"
	ExecTrigComplete      ExecTrigger = "Complete"
	ExecTrigFail          ExecTrigger = "Fail"
	ExecTrigInputRequired ExecTrigger = "InputRequired"
	ExecTrigVMCrash       ExecTrigger = "VMCrash"
	ExecTrigCancel        ExecTrigger = "Cancel"
)
