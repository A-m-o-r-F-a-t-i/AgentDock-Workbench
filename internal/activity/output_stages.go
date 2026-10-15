package activity

// Pointers preserve unknown values in historical records. These are bounded
// model delivery facts, separate from the immutable source payload descriptor.
type OutputStages struct {
	Operation       *OutputOperation       `json:"operation,omitempty"`
	Capture         *OutputCapture         `json:"capture,omitempty"`
	Projection      *OutputProjection      `json:"projection,omitempty"`
	Delivery        *OutputDelivery        `json:"delivery,omitempty"`
	ModelProjection *ProjectionMeasurement `json:"model_projection,omitempty"`
	ActivityPreview *PreviewMeasurement    `json:"activity_preview,omitempty"`
}

type OutputOperation struct {
	State     string `json:"state"`
	OK        *bool  `json:"ok"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	TimedOut  bool   `json:"timed_out,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

type OutputCapture struct {
	State         string `json:"state"`
	Complete      *bool  `json:"complete"`
	RetainedBytes int64  `json:"retained_bytes,omitempty"`
	DroppedBytes  int64  `json:"dropped_bytes,omitempty"`
	SourceRef     string `json:"source_ref,omitempty"`
}

type OutputProjection struct {
	Mode         string `json:"mode"`
	Complete     bool   `json:"complete"`
	VisibleChars int    `json:"visible_chars,omitempty"`
	OmittedBytes int64  `json:"omitted_bytes,omitempty"`
}

type OutputDelivery struct {
	Class                 string `json:"class"`
	SequenceComplete      bool   `json:"sequence_complete"`
	ContinuationRequired  bool   `json:"continuation_required"`
	ContinuationAvailable bool   `json:"continuation_available"`
	PageIndex             int    `json:"page_index,omitempty"`
	PageCount             int    `json:"page_count,omitempty"`
	SnapshotID            string `json:"snapshot_id,omitempty"`
}

type ProjectionMeasurement struct {
	Chars                 int    `json:"chars"`
	BudgetChars           int    `json:"budget_chars"`
	Class                 string `json:"class"`
	Mode                  string `json:"mode"`
	Complete              bool   `json:"complete"`
	NativeAttachmentBytes int64  `json:"native_attachment_bytes,omitempty"`
}

type PreviewMeasurement struct {
	Chars     int  `json:"chars"`
	Truncated bool `json:"truncated"`
}

func (s *OutputStages) clone() *OutputStages {
	if s == nil {
		return nil
	}
	copy := *s
	copy.Operation = copyValue(s.Operation)
	if copy.Operation != nil {
		copy.Operation.OK = copyValue(copy.Operation.OK)
		copy.Operation.ExitCode = copyValue(copy.Operation.ExitCode)
	}
	copy.Capture = copyValue(s.Capture)
	if copy.Capture != nil {
		copy.Capture.Complete = copyValue(copy.Capture.Complete)
	}
	copy.Projection, copy.Delivery = copyValue(s.Projection), copyValue(s.Delivery)
	copy.ModelProjection, copy.ActivityPreview = copyValue(s.ModelProjection), copyValue(s.ActivityPreview)
	return &copy
}
