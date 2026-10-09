package novel

// ValueError marks the business validation failures the reference raises as
// ValueError: the create endpoint maps them to 400100 and the upload endpoint
// to 400102.
type ValueError struct{ Message string }

func (e *ValueError) Error() string { return e.Message }

// VisualizationError mirrors services.NovelVisualizationError.
type VisualizationError struct{ Message string }

func (e *VisualizationError) Error() string { return e.Message }
