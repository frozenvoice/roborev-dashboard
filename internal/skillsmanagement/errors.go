package skillsmanagement

import "errors"

var (
	ErrUnsupportedAgent  = errors.New("unsupported skills agent")
	ErrRelativeTarget    = errors.New("skills target is not absolute")
	ErrConfigUnavailable = errors.New("skills agent config is unavailable")
	ErrLegacyPresent     = errors.New("legacy RoboRev skill cleanup requires manual CLI action")
	ErrTreeChanged       = errors.New("skills target tree changed")
	ErrTreeUnsafe        = errors.New("skills target tree is unsafe")
	ErrAggregateTooLarge = errors.New("skills target tree exceeds the size limit")
)
