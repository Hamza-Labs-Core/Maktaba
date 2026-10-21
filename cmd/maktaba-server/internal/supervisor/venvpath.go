package supervisor

// VenvPython is a venv's interpreter on this OS (bin/python, or
// Scripts\python.exe on Windows): the desktop app's setup runs the pipeline's
// models download with it (Epic 38 story 38.6).
func VenvPython(venv string) string { return venvPython(venv) }
