/*
Copyright The CloudNativePG Contributors
Copyright 2025, Opera Norway AS

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

package common

import (
	"encoding/json"
	"os"
	"path/filepath"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cnpg-i/pkg/wal"
	machineryapi "github.com/cloudnative-pg/machinery/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pgbackrestv1 "github.com/operasoftware/cnpg-plugin-pgbackrest/api/v1"
	"github.com/operasoftware/cnpg-plugin-pgbackrest/internal/cnpgi/metadata"
	pgbackrestApi "github.com/operasoftware/cnpg-plugin-pgbackrest/internal/pgbackrest/api"
	pgbackrestCredentials "github.com/operasoftware/cnpg-plugin-pgbackrest/internal/pgbackrest/credentials"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("WAL archive", func() {
	It("passes the backup CA bundle environment to pgbackrest archive commands", func(ctx SpecContext) {
		tempDir := GinkgoT().TempDir()
		pgData := filepath.Join(tempDir, "pgdata")
		Expect(os.MkdirAll(filepath.Join(pgData, "pg_wal", "archive_status"), 0755)).To(Succeed())

		sourceWAL := filepath.Join(pgData, "pg_wal", "000000010000000000000001")
		Expect(os.WriteFile(sourceWAL, []byte("wal"), 0600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(pgData, metadata.CheckEmptyWalArchiveFile), nil, 0600)).To(Succeed())

		capturePath := filepath.Join(tempDir, "pgbackrest-env")
		writeFakePgbackrest(tempDir)
		prependPath(tempDir)
		setEnv("PGDATA", pgData)
		setEnv("PGBACKREST_CAPTURE_ENV", capturePath)

		cluster := &cnpgv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "cluster-example",
			},
			Spec: cnpgv1.ClusterSpec{
				Plugins: []cnpgv1.PluginConfiguration{
					{
						Name: metadata.PluginName,
						Parameters: map[string]string{
							"pgbackrestObjectName": "archive-example",
							"stanza":               "cluster-example",
						},
					},
				},
			},
		}
		clusterDefinition, err := json.Marshal(cluster)
		Expect(err).ToNot(HaveOccurred())

		scheme := runtime.NewScheme()
		Expect(pgbackrestv1.AddToScheme(scheme)).To(Succeed())
		client := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(newArchiveWithEndpointCA()).
			Build()

		service := WALServiceImplementation{
			Client:         client,
			SpoolDirectory: filepath.Join(tempDir, "spool"),
			PGDataPath:     pgData,
		}
		_, err = service.Archive(ctx, &wal.WALArchiveRequest{
			ClusterDefinition: clusterDefinition,
			SourceFileName:    sourceWAL,
		})
		Expect(err).ToNot(HaveOccurred())

		expectCapturedBackupCAEnvironment(capturePath)
	})

	It("passes the backup CA bundle environment to pgbackrest status commands", func(ctx SpecContext) {
		tempDir := GinkgoT().TempDir()
		capturePath := filepath.Join(tempDir, "pgbackrest-env")
		writeFakePgbackrest(tempDir)
		prependPath(tempDir)
		setEnv("PGBACKREST_CAPTURE_ENV", capturePath)

		cluster := &cnpgv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "cluster-example",
			},
			Spec: cnpgv1.ClusterSpec{
				Plugins: []cnpgv1.PluginConfiguration{
					{
						Name: metadata.PluginName,
						Parameters: map[string]string{
							"pgbackrestObjectName": "archive-example",
							"stanza":               "cluster-example",
						},
					},
				},
			},
		}
		clusterDefinition, err := json.Marshal(cluster)
		Expect(err).ToNot(HaveOccurred())

		scheme := runtime.NewScheme()
		Expect(pgbackrestv1.AddToScheme(scheme)).To(Succeed())
		client := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(newArchiveWithEndpointCA()).
			Build()

		service := WALServiceImplementation{
			Client: client,
		}
		status, err := service.Status(ctx, &wal.WALStatusRequest{
			ClusterDefinition: clusterDefinition,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(status.FirstWal).To(Equal("000000010000000000000001"))
		Expect(status.LastWal).To(Equal("000000010000000000000002"))

		expectCapturedBackupCAEnvironment(capturePath)
	})
})

func newArchiveWithEndpointCA() *pgbackrestv1.Archive {
	return &pgbackrestv1.Archive{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "archive-example",
		},
		Spec: pgbackrestv1.ArchiveSpec{
			Configuration: pgbackrestApi.PgbackrestConfiguration{
				Repositories: []pgbackrestApi.PgbackrestRepository{
					{
						PgbackrestCredentials: pgbackrestApi.PgbackrestCredentials{
							AWS: &pgbackrestApi.S3Credentials{
								KeyType: pgbackrestApi.KeyTypeAuto,
								Region:  "us-east-1",
							},
						},
						EndpointCA: &machineryapi.SecretKeySelector{
							LocalObjectReference: machineryapi.LocalObjectReference{
								Name: "ca-secret",
							},
							Key: "ca.crt",
						},
						Bucket:          "bucket",
						DestinationPath: "/",
					},
				},
			},
		},
	}
}

func writeFakePgbackrest(directory string) {
	fakePgbackrest := filepath.Join(directory, "pgbackrest")
	script := `#!/bin/sh
env | grep -E '^(AWS_CA_BUNDLE|PGBACKREST_REPO1_HOST_CA_FILE)=' >> "$PGBACKREST_CAPTURE_ENV" || true
if [ "$1" = "info" ]; then
  cat <<'JSON'
[{
  "archive": [{
    "database": { "id": 1, "repo_key": 1 },
    "id": "15-1",
    "max": "000000010000000000000002",
    "min": "000000010000000000000001"
  }],
  "backup": [],
  "cipher": "none",
  "db": [],
  "name": "cluster-example",
  "status": { "code": 2, "message": "no valid backups" }
}]
JSON
fi
exit 0
`
	Expect(os.WriteFile(fakePgbackrest, []byte(script), 0755)).To(Succeed())
}

func expectCapturedBackupCAEnvironment(capturePath string) {
	capturedEnv, err := os.ReadFile(capturePath)
	Expect(err).ToNot(HaveOccurred())
	env := string(capturedEnv)
	Expect(env).To(ContainSubstring(
		"AWS_CA_BUNDLE=" + pgbackrestCredentials.BarmanBackupEndpointCACertificateLocation))
	Expect(env).To(ContainSubstring(
		"PGBACKREST_REPO1_HOST_CA_FILE=" + pgbackrestCredentials.BarmanBackupEndpointCACertificateLocation))
}

func prependPath(directory string) {
	oldPath := os.Getenv("PATH")
	Expect(os.Setenv("PATH", directory+string(os.PathListSeparator)+oldPath)).To(Succeed())
	DeferCleanup(func() {
		Expect(os.Setenv("PATH", oldPath)).To(Succeed())
	})
}

func setEnv(key string, value string) {
	oldValue, hadValue := os.LookupEnv(key)
	Expect(os.Setenv(key, value)).To(Succeed())
	DeferCleanup(func() {
		if hadValue {
			Expect(os.Setenv(key, oldValue)).To(Succeed())
			return
		}
		Expect(os.Unsetenv(key)).To(Succeed())
	})
}
