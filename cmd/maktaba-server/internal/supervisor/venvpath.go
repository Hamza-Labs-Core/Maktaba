package supervisor

// VenvPython is a venv's interpreter on this OS (bin/python, or
// Scripts\python.exe on Windows): the desktop app's setup runs the pipeline's
// models download with it (Epic 38 story 38.6).
func VenvPython(venv string) string { return venvPython(venv) }

// RunsFromVenv reports whether p runs from the pipeline's Python environment
// (pythonFor: the desktop app's venv, a plain server release's <release>/venv): the
// pipeline's processes (the main worker, the transcription workers, the index
// worker, the Chroma copy) and every plugin runner. Such a child cannot start before
// that environment holds this release's packages, so the desktop app gates every one
// of them on its venv step (serve_appbundle.go); started before it, the child dies on
// a missing module and its exits feed the crash loop (the 0.3.96 update: the plugin
// runners, gated on the database alone, were marked failed while the venv installed).
func RunsFromVenv(p Process) bool {
	return p.Role == RolePipeline || p.Role == RolePluginRunner
}
