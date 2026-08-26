package skillsmanagement

// ReadinessCounts is the minimal aggregate used to classify a detached
// install readback. It intentionally contains no skill names or file content.
type ReadinessCounts struct {
	Current  int
	Outdated int
	Missing  int
}

// Improvement returns the change in authoritative installed readiness. A
// positive value means current increased or outstanding work decreased.
func Improvement(before, after ReadinessCounts) int {
	return (after.Current - before.Current) + ((before.Outdated + before.Missing) - (after.Outdated + after.Missing))
}

func HasMeasurableImprovement(before, after ReadinessCounts) bool {
	return Improvement(before, after) > 0
}
