package service

// systemdSystem: the system unit (design §2.3). KillMode=mixed: systemd sends
// SIGTERM to the launcher only, which stops the server; children are not
// killed behind its back (a supervisor restart keeps TV running, §8).
// Restart=always restarts the launcher; RestartForceExitStatus=75 also
// restarts it when it exits 75 (a launcher replacement, §8.3).
const systemdSystem = `[Unit]
Description=Maktaba (launcher: current release of maktaba-server)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User={{.User}}
Group={{.Group}}
{{- if .ConfigPath}}
Environment=MAKTABA_CONFIG={{.ConfigPath}}
{{- end}}
ExecStart={{.Launcher}} --root {{.Root}} serve
Restart=always
RestartSec=5
RestartForceExitStatus=75
KillMode=mixed
TimeoutStopSec=90
LimitNOFILE=65536

# Hardening (Story 23). Only the install root and the log directory are
# writable (ProtectSystem=strict): releases live under the root, never /usr.
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
ReadWritePaths={{join .ReadWritePaths " "}}

[Install]
WantedBy=multi-user.target
`

// systemdUser: a user unit (nodes, as Epic 31; linger keeps it running
// without a session).
const systemdUser = `[Unit]
Description=Maktaba (launcher: current release of maktaba-server)
After=network-online.target

[Service]
Type=simple
{{- if .ConfigPath}}
Environment=MAKTABA_CONFIG={{.ConfigPath}}
{{- end}}
ExecStart={{.Launcher}} --root {{.Root}} serve
Restart=always
RestartSec=5
RestartForceExitStatus=75
KillMode=mixed
TimeoutStopSec=90

[Install]
WantedBy=default.target
`

// launchdPlist: LaunchDaemon (home) or LaunchAgent (node). AbandonProcessGroup:
// launchd does not kill the launcher's children when it exits (§8).
const launchdPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.maktaba.server</string>

  <key>ProgramArguments</key>
  <array>
    <string>{{xml .Launcher}}</string>
    <string>--root</string>
    <string>{{xml .Root}}</string>
    <string>serve</string>
  </array>

  <key>RunAtLoad</key>
  <true/>

  <key>KeepAlive</key>
  <true/>

  <key>AbandonProcessGroup</key>
  <true/>

  <key>ExitTimeOut</key>
  <integer>90</integer>

  <key>StandardOutPath</key>
  <string>{{xml .LogDir}}/server.log</string>

  <key>StandardErrorPath</key>
  <string>{{xml .LogDir}}/server.err.log</string>

  <key>WorkingDirectory</key>
  <string>{{xml .Root}}</string>
{{- if .ConfigPath}}

  <key>EnvironmentVariables</key>
  <dict>
    <key>MAKTABA_CONFIG</key>
    <string>{{xml .ConfigPath}}</string>
  </dict>
{{- end}}
</dict>
</plist>
`

// taskXML: the Windows logon task (default on Windows: mapped drives are per
// user, Epic 31). It restarts the launcher every minute on failure (3 tries);
// the launcher handles exit 75 itself. No time limit; one instance.
const taskXML = `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Maktaba: runs maktaba-launcher, which starts the current release of maktaba-server.</Description>
    <URI>\Maktaba</URI>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
{{- if .WinUser}}
      <UserId>{{xml .WinUser}}</UserId>
{{- end}}
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
{{- if .WinUser}}
      <UserId>{{xml .WinUser}}</UserId>
{{- end}}
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>3</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>{{xml .Launcher}}</Command>
      <Arguments>--root {{xml (winarg .Root)}} --hide-console serve</Arguments>
      <WorkingDirectory>{{xml .Root}}</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
`

const winServiceText = `Windows service {{"Maktaba"}}
  binary:   {{winarg .Launcher}} --root {{winarg .Root}} serve
  start:    automatic
  recovery: restart after 5 s; reset the failure count after 1 day
`
