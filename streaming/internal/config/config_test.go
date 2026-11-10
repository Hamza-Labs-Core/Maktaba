package config

import "testing"

// Epic 59 story 59.05: the video settings the supervisor passes (the tier's transcode
// limits, VA-API first, the render node, the verdict's file) are read; unset, none.
func TestLoadVideoSettings(t *testing.T) {
	c := Load()
	if c.Transcode.SoftwareMax != 0 || c.HWAccel.PreferVAAPI || c.HWAccel.RenderDevice != "" || c.HWAccel.StatusFile != "" {
		t.Fatalf("unset: %+v %+v", c.Transcode, c.HWAccel)
	}
	for k, v := range map[string]string{
		"MAKTABA_TRANSCODE_SOFTWARE_MAX":        "1",
		"MAKTABA_TRANSCODE_SOFTWARE_MAX_HEIGHT": "720",
		"MAKTABA_TRANSCODE_HARDWARE_MAX":        "2",
		"MAKTABA_HWACCEL_VAAPI_FIRST":           "1",
		"MAKTABA_RENDER_DEVICE":                 "/dev/dri/renderD129",
		"MAKTABA_VIDEO_STATUS_FILE":             "/state/streaming/video.json",
	} {
		t.Setenv(k, v)
	}
	c = Load()
	if c.Transcode.SoftwareMax != 1 || c.Transcode.SoftwareMaxHeight != 720 || c.Transcode.HardwareMax != 2 ||
		!c.HWAccel.PreferVAAPI || c.HWAccel.RenderDevice != "/dev/dri/renderD129" || c.HWAccel.StatusFile != "/state/streaming/video.json" {
		t.Fatalf("set: %+v %+v", c.Transcode, c.HWAccel)
	}
}
