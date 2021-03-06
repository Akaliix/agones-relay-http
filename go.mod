module github.com/Octops/agones-relay-http

go 1.14

require (
	agones.dev/agones v1.9.0
	github.com/Octops/agones-event-broadcaster v0.1.9-alpha.4
	github.com/labstack/echo/v4 v4.1.17
	github.com/mitchellh/go-homedir v1.1.0
	github.com/pkg/errors v0.8.1
	github.com/sirupsen/logrus v1.4.2
	github.com/spf13/cobra v1.0.0
	github.com/spf13/viper v1.7.1
	github.com/stretchr/testify v1.5.0
	k8s.io/apimachinery v0.17.2
	k8s.io/client-go v0.17.2
)
