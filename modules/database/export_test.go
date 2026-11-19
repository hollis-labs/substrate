package database

// SetDeployStep makes Start behave as if the process were (or were not) `<app> db migrate ...`; it returns the restore.
func SetDeployStep(on bool) func() {
	old := argsForDeployStep
	argsForDeployStep = func() []string {
		if on {
			return []string{"db", "migrate", "--store", "x"}
		}
		return nil
	}
	return func() { argsForDeployStep = old }
}
