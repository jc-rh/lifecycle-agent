package installationiso

import (
	"errors"
	"fmt"
	"github.com/openshift-kni/lifecycle-agent/api/ibiconfig"
	"github.com/openshift-kni/lifecycle-agent/utils"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"github.com/openshift-kni/lifecycle-agent/lca-cli/ops"
)

func TestInstallationIso(t *testing.T) {
	var ()

	testcases := []struct {
		name                string
		workDirExists       bool
		authFileExists      bool
		pullSecretExists    bool
		sshPublicKeyExists  bool
		liveIsoUrlSuccess   bool
		precacheBestEffort  bool
		precacheDisabled    bool
		shutdown            bool
		skipDiskCleanup     bool
		renderCommandReturn error
		embedCommandReturn  error
		expectedError       string
	}{
		{
			name:               "Happy flow",
			workDirExists:      true,
			authFileExists:     true,
			pullSecretExists:   true,
			sshPublicKeyExists: true,
			liveIsoUrlSuccess:  true,
			precacheBestEffort: false,
			precacheDisabled:   false,
			shutdown:           false,
			skipDiskCleanup:    false,
			expectedError:      "",
		},
		{
			name:               "Happy flow - precache best-effort set",
			workDirExists:      true,
			authFileExists:     true,
			pullSecretExists:   true,
			sshPublicKeyExists: true,
			liveIsoUrlSuccess:  true,
			precacheBestEffort: true,
			precacheDisabled:   false,
			shutdown:           false,
			skipDiskCleanup:    false,
			expectedError:      "",
		},
		{
			name:               "Happy flow - precache disabled set",
			workDirExists:      true,
			authFileExists:     true,
			pullSecretExists:   true,
			sshPublicKeyExists: true,
			liveIsoUrlSuccess:  true,
			precacheBestEffort: false,
			precacheDisabled:   true,
			shutdown:           false,
			skipDiskCleanup:    false,
			expectedError:      "",
		},
		{
			name:               "Happy flow - shutdown set",
			workDirExists:      true,
			authFileExists:     true,
			pullSecretExists:   true,
			sshPublicKeyExists: true,
			liveIsoUrlSuccess:  true,
			precacheBestEffort: false,
			precacheDisabled:   false,
			shutdown:           true,
			skipDiskCleanup:    false,
			expectedError:      "",
		},
		{
			name:               "Happy flow - skipDiskCleanup set",
			workDirExists:      true,
			authFileExists:     true,
			pullSecretExists:   true,
			sshPublicKeyExists: true,
			liveIsoUrlSuccess:  true,
			precacheBestEffort: false,
			precacheDisabled:   false,
			shutdown:           false,
			skipDiskCleanup:    true,
			expectedError:      "",
		},
		{
			name:               "missing workdir",
			workDirExists:      false,
			authFileExists:     false,
			pullSecretExists:   false,
			sshPublicKeyExists: false,
			liveIsoUrlSuccess:  false,
			precacheBestEffort: false,
			precacheDisabled:   false,
			shutdown:           false,
			skipDiskCleanup:    false,
			expectedError:      "work dir doesn't exists",
		},
		{
			name:               "missing authFile",
			workDirExists:      true,
			authFileExists:     false,
			pullSecretExists:   true,
			sshPublicKeyExists: true,
			liveIsoUrlSuccess:  true,
			precacheBestEffort: false,
			precacheDisabled:   false,
			shutdown:           false,
			skipDiskCleanup:    false,
			expectedError:      "authFile: no such file or directory",
		},
		{
			name:               "missing psFile",
			workDirExists:      true,
			authFileExists:     true,
			pullSecretExists:   false,
			sshPublicKeyExists: true,
			liveIsoUrlSuccess:  true,
			precacheBestEffort: false,
			precacheDisabled:   false,
			shutdown:           false,
			skipDiskCleanup:    false,
			expectedError:      "psFile: no such file or directory",
		},
		{
			name:               "missing ssh key",
			workDirExists:      true,
			authFileExists:     true,
			pullSecretExists:   true,
			sshPublicKeyExists: false,
			liveIsoUrlSuccess:  true,
			precacheBestEffort: false,
			precacheDisabled:   false,
			shutdown:           false,
			skipDiskCleanup:    false,
			expectedError:      "sshKey: no such file or directory",
		},
		{
			name:               "Failed to download rhcos",
			workDirExists:      true,
			authFileExists:     true,
			pullSecretExists:   true,
			sshPublicKeyExists: true,
			liveIsoUrlSuccess:  false,
			precacheBestEffort: false,
			precacheDisabled:   false,
			shutdown:           false,
			skipDiskCleanup:    false,
			expectedError:      "notfound",
		},
		{
			name:                "Render failure",
			workDirExists:       true,
			authFileExists:      true,
			pullSecretExists:    true,
			sshPublicKeyExists:  true,
			liveIsoUrlSuccess:   false,
			precacheBestEffort:  false,
			precacheDisabled:    false,
			shutdown:            false,
			renderCommandReturn: errors.New("failed to render ignition config"),
			expectedError:       "failed to render ignition config",
		},
		{
			name:                "embed failure",
			workDirExists:       true,
			authFileExists:      true,
			pullSecretExists:    true,
			sshPublicKeyExists:  true,
			liveIsoUrlSuccess:   false,
			precacheBestEffort:  false,
			precacheDisabled:    false,
			shutdown:            false,
			skipDiskCleanup:     false,
			renderCommandReturn: errors.New("failed to embed ignition config to ISO"),
			expectedError:       "failed to embed ignition config to ISO",
		},
	}
	var (
		mockController      = gomock.NewController(t)
		mockOps             = ops.NewMockOps(mockController)
		seedImage           = "seedImage"
		seedVersion         = "seedVersion"
		installationDisk    = "/dev/sda"
		extraPartitionStart = "-40G"
	)

	for _, tc := range testcases {
		tmpDir := "noSuchDir"
		if tc.workDirExists {
			tmpDir = t.TempDir()
		}
		t.Run(tc.name, func(t *testing.T) {
			log := &logrus.Logger{}
			sshPublicKeyPath := "sshKey"
			if tc.sshPublicKeyExists {
				sshPublicKey, err := os.Create(path.Join(tmpDir, sshPublicKeyPath))
				assert.Equal(t, err, nil)
				sshPublicKeyPath = sshPublicKey.Name()
			}
			testAuthFilePath := "authFile"
			if tc.authFileExists {
				authFile, err := os.Create(path.Join(tmpDir, testAuthFilePath))
				assert.Equal(t, err, nil)
				testAuthFilePath = authFile.Name()
			}
			testPSFilePath := "psFile"
			if tc.pullSecretExists {
				psFile, err := os.Create(path.Join(tmpDir, testPSFilePath))
				assert.Equal(t, err, nil)
				testPSFilePath = psFile.Name()
			}
			if tc.pullSecretExists && tc.authFileExists && tc.sshPublicKeyExists {
				mockOps.EXPECT().RunInHostNamespace("podman", "run",
					"-v", fmt.Sprintf("%s:/data:rw,Z", tmpDir),
					"--rm",
					"quay.io/coreos/butane:release",
					"--pretty", "--strict",
					"-d", "/data",
					path.Join("/data", butaneConfigFile),
					"-o", path.Join("/data", ibiIgnitionFileName)).Return("", tc.renderCommandReturn).Times(1)
				if tc.liveIsoUrlSuccess {
					mockOps.EXPECT().RunInHostNamespace("podman", "run",
						"-v", fmt.Sprintf("%s:/data:rw,Z", tmpDir),
						coreosInstallerImage,
						"iso", "ignition", "embed",
						"-i", path.Join("/data", ibiIgnitionFileName),
						"-o", path.Join("/data", ibiIsoFileName),
						path.Join("/data", rhcosIsoFileName)).Return("", tc.embedCommandReturn).Times(1)
				}
			}
			rhcosLiveIsoUrl := "notfound"
			if tc.liveIsoUrlSuccess {
				server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
					rw.Write([]byte(`rhcos-live-iso`))
				}))
				rhcosLiveIsoUrl = server.URL
				defer server.Close()
			}
			ibiConfig := &ibiconfig.IBIPrepareConfig{
				PrecacheDisabled:    tc.precacheDisabled,
				PrecacheBestEffort:  tc.precacheBestEffort,
				Shutdown:            tc.shutdown,
				SkipDiskCleanup:     tc.skipDiskCleanup,
				SeedImage:           seedImage,
				SeedVersion:         seedVersion,
				AuthFile:            testAuthFilePath,
				PullSecretFile:      testPSFilePath,
				SSHPublicKeyFile:    sshPublicKeyPath,
				RHCOSLiveISO:        rhcosLiveIsoUrl,
				InstallationDisk:    installationDisk,
				ExtraPartitionStart: extraPartitionStart,
			}

			installationIso := NewInstallationIso(log, mockOps, tmpDir)
			err := installationIso.Create(ibiConfig)
			if tc.expectedError == "" {
				assert.Equal(t, err, nil)
				var ibiConfig ibiconfig.IBIPrepareConfig
				errReading := utils.ReadYamlOrJSONFile(path.Join(tmpDir, butaneFiles, ibiConfigFileName), &ibiConfig)
				assert.Equal(t, errReading, nil)
				assert.Equal(t, ibiConfig.PrecacheDisabled, tc.precacheDisabled)
				assert.Equal(t, ibiConfig.PrecacheBestEffort, tc.precacheBestEffort)
				assert.Equal(t, ibiConfig.Shutdown, tc.shutdown)
				assert.Equal(t, ibiConfig.SkipDiskCleanup, tc.skipDiskCleanup)
				assert.Equal(t, ibiConfig.AuthFile, authIgnitionFilePath)
				assert.Equal(t, ibiConfig.PullSecretFile, psIgnitioFilePath)
			} else {
				assert.Contains(t, err.Error(), tc.expectedError)
			}

		})
	}
}
