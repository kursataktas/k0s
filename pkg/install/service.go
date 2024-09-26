/*
Copyright 2020 k0s authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package install

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kardianos/service"
	"github.com/sirupsen/logrus"
)

var (
	k0sServiceName = "k0s"
	k0sDescription = "k0s - Zero Friction Kubernetes"
)

type Program struct{}

func (p *Program) Start(service.Service) error {
	// Start should not block. Do the actual work async.
	return nil
}

func (p *Program) Stop(service.Service) error {
	// Stop should not block. Return with a few seconds.
	return nil
}

// InstalledService returns a k0s service if one has been installed on the host or an error otherwise.
func InstalledService() (service.Service, error) {
	prg := &Program{}
	for _, role := range []string{"controller", "worker"} {
		c := GetServiceConfig(role)
		s, err := service.New(prg, c)
		if err != nil {
			return nil, err
		}
		_, err = s.Status()

		if err != nil && errors.Is(err, service.ErrNotInstalled) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return s, nil
	}

	var s service.Service
	return s, fmt.Errorf("k0s has not been installed as a service")
}

// EnsureService installs the k0s service, per the given arguments, and the detected platform
func EnsureService(args []string, envVars []string, force bool) error {
	var deps []string
	var svcConfig *service.Config

	prg := &Program{}
	for _, v := range args {
		if v == "controller" || v == "worker" {
			svcConfig = GetServiceConfig(v)
			break
		}
	}

	s, err := service.New(prg, svcConfig)
	if err != nil {
		return err
	}

	// fetch service type
	svcType := s.Platform()
	switch svcType {
	case "linux-openrc":
		deps = []string{"need cgroups", "need net", "use dns", "after firewall"}
		svcConfig.Option = map[string]interface{}{
			"OpenRCScript": openRCScript,
		}
	case "linux-upstart":
		svcConfig.Option = map[string]interface{}{
			"UpstartScript": upstartScript,
		}
	case "unix-systemv":
		svcConfig.Option = map[string]interface{}{
			"SystemdScript": sysvScript,
		}
	case "linux-systemd":
		deps = []string{"After=network-online.target", "Wants=network-online.target"}
		svcConfig.Option = map[string]interface{}{
			"SystemdScript": systemdScript,
			"LimitNOFILE":   999999,
		}
	default:
	}

	if len(envVars) > 0 {
		svcConfig.Option["Environment"] = envVars
	}

	svcConfig.Dependencies = deps
	svcConfig.Arguments = args
	if force {
		logrus.Infof("Uninstalling %s service", svcConfig.Name)
		err = s.Uninstall()
		if err != nil && !errors.Is(err, service.ErrNotInstalled) {
			logrus.Warnf("failed to uninstall service: %v", err)
		}
	}
	logrus.Infof("Installing %s service", svcConfig.Name)
	err = s.Install()
	if err != nil {
		return fmt.Errorf("failed to install service: %w", err)
	}
	return nil
}

func UninstallService(role string) error {
	prg := &Program{}

	if role == "controller+worker" {
		role = "controller"
	}

	svcConfig := GetServiceConfig(role)
	s, err := service.New(prg, svcConfig)
	if err != nil {
		return err
	}

	return s.Uninstall()
}

func GetServiceConfig(role string) *service.Config {
	var k0sDisplayName string

	if role == "controller" || role == "worker" {
		k0sDisplayName = "k0s " + role
		k0sServiceName = "k0s" + role
	}
	return &service.Config{
		Name:        k0sServiceName,
		DisplayName: k0sDisplayName,
		Description: k0sDescription,
	}
}

func prepareEnvVars(envVars []string) map[string]string {
	result := make(map[string]string)
	for _, envVar := range envVars {
		parts := strings.SplitN(envVar, "=", 1)
		if len(parts) != 2 {
			continue
		}

		result[parts[0]] = parts[1]
	}
	return result
}

// Upstream kardianos/service does not support all the options we want to set to the systemd unit, hence we override the template
// Currently mostly for KillMode=process so we get systemd to only send the sigterm to the main process
const systemdScript = `[Unit]
Description={{.Description}}
Documentation=https://docs.k0sproject.io
ConditionFileIsExecutable={{.Path|cmdEscape}}
{{range $i, $dep := .Dependencies}}
{{$dep}} {{end}}

[Service]
StartLimitInterval=5
StartLimitBurst=10
ExecStart={{.Path|cmdEscape}}{{range .Arguments}} {{.|cmdEscape}}{{end}}
{{- if .Option.Environment}}{{range .Option.Environment}}
Environment="{{.}}"{{end}}{{- end}}

RestartSec=10
Delegate=yes
KillMode=process
LimitCORE=infinity
TasksMax=infinity
TimeoutStartSec=0

{{- if .ChRoot}}RootDirectory={{.ChRoot|cmd}}{{- end}}

{{- if .WorkingDirectory}}WorkingDirectory={{.WorkingDirectory|cmdEscape}}{{- end}}
{{- if .UserName}}User={{.UserName}}{{end}}
{{- if .ReloadSignal}}ExecReload=/bin/kill -{{.ReloadSignal}} "$MAINPID"{{- end}}
{{- if .PIDFile}}PIDFile={{.PIDFile|cmd}}{{- end}}
{{- if and .LogOutput .HasOutputFileSupport -}}
StandardOutput=file:/var/log/{{.Name}}.out
StandardError=file:/var/log/{{.Name}}.err
{{- end}}

{{- if .SuccessExitStatus}}SuccessExitStatus={{.SuccessExitStatus}}{{- end}}
{{ if gt .LimitNOFILE -1 }}LimitNOFILE={{.LimitNOFILE}}{{- end}}
{{ if .Restart}}Restart={{.Restart}}{{- end}}

[Install]
WantedBy=multi-user.target
`
