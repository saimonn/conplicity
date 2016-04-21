package providers

import (
	"os"
	"strings"
	"unicode/utf8"
	log "github.com/Sirupsen/logrus"
	"github.com/fsouza/go-dockerclient"
	"github.com/fgrehm/go-dockerpty"
  "github.com/camptocamp/conplicity/handler"
)

const labelPrefix string = "io.conplicity"

type DefaultProvider struct {
  handler *handler.Conplicity
  vol *docker.Volume
}

func (*DefaultProvider) PrepareBackup() error {
  return nil
}

func (p *DefaultProvider) BackupVolume() (err error) {
  vol := p.vol
  c := p.handler
	if utf8.RuneCountInString(vol.Name) == 64 {
		log.Infof("Ignoring unnamed volume " + vol.Name)
		return
	}

	if getVolumeLabel(vol, ".ignore") == "true" {
		log.Infof("Ignoring blacklisted volume " + vol.Name)
		return
	}

	// TODO: detect if it's a Database volume (PostgreSQL, MySQL, OpenLDAP...) and launch DUPLICITY_PRECOMMAND instead of backuping the volume
	log.Infof("ID: " + vol.Name)
	log.Infof("Driver: " + vol.Driver)
	log.Infof("Mountpoint: " + vol.Mountpoint)

	backupDir := ""
	log.Infof("Detect volume content...")
	// TODO: Needs refactoring to avoid code duplication
	// FIXME: this will only work will local volume driver and root access,
	// we need to use something like "docker run -v vol.Name:/foo busybox test -f /foo/PG_VERSION"
	if f, err := os.Stat(vol.Mountpoint + "/PG_VERSION"); err == nil && f.Mode().IsRegular() {
		log.Infof("PG_VERSION file found, this should be a PostgreSQL datadir")
		log.Infof("Searching postgres container using this volume...")
		containers, _ := c.ListContainers(docker.ListContainersOptions{})
		for _, container := range containers {
			for _, mount := range container.Mounts {
				if mount.Name == vol.Name {
					log.Infof("Volume %v is used by container %v", vol.Name, container.ID)
					log.Infof("Launch pg_dumpall in container %v...", container.ID)
					exec, err := c.CreateExec(
						docker.CreateExecOptions{
							Container: container.ID,
							Cmd: []string{
								"sh",
								"-c",
								"mkdir -p " + mount.Destination + "/backups && pg_dumpall -Upostgres > " + mount.Destination + "/backups/all.sql",
							},
						},
					)

					checkErr(err, "Failed to create exec", 1)

					err = c.StartExec(
						exec.ID,
						docker.StartExecOptions{},
					)

					checkErr(err, "Failed to create exec", 1)

					backupDir = "backups"
				}
			}
		}
	} else if f, err := os.Stat(vol.Mountpoint + "/mysql"); err == nil && f.Mode().IsDir() {
		log.Infof("mysql directory found, this should be MySQL datadir")
		log.Infof("Searching mysql container using this volume...")
		containers, _ := c.ListContainers(docker.ListContainersOptions{})
		for _, container := range containers {
			for _, mount := range container.Mounts {
				if mount.Name == vol.Name {
					log.Infof("Volume %v is used by container %v", vol.Name, container.ID)
					log.Infof("Launch mysqldump in container %v...", container.ID)
					exec, err := c.CreateExec(
						docker.CreateExecOptions{
							Container: container.ID,
							Cmd: []string{
								"sh",
								"-c",
								"mkdir -p " + mount.Destination + "/backups && mysqldump --all-databases --extended-insert --password=$MYSQL_ROOT_PASSWORD > " + mount.Destination + "/backups/all.sql",
							},
						},
					)

					checkErr(err, "Failed to create exec", 1)

					err = c.StartExec(
						exec.ID,
						docker.StartExecOptions{},
					)

					checkErr(err, "Failed to create exec", 1)

					backupDir = "backups"
				}
			}
		}
	} else if f, err := os.Stat(vol.Mountpoint + "/DB_CONFIG"); err == nil && f.Mode().IsRegular() {
		log.Infof("DB_CONFIG file found, this should be and OpenLDAP datadir")
		log.Infof("Searching OpenLDAP container using this volume...")
		containers, _ := c.ListContainers(docker.ListContainersOptions{})
		for _, container := range containers {
			for _, mount := range container.Mounts {
				if mount.Name == vol.Name {
					log.Infof("Volume %v is used by container %v", vol.Name, container.ID)
					log.Infof("Launch slapcat in container %v...", container.ID)
					exec, err := c.CreateExec(
						docker.CreateExecOptions{
							Container: container.ID,
							Cmd: []string{
								"sh",
								"-c",
								"mkdir -p " + mount.Destination + "/backups && slapcat > " + mount.Destination + "/backups/all.ldif",
							},
						},
					)

					checkErr(err, "Failed to create exec", 1)

					err = c.StartExec(
						exec.ID,
						docker.StartExecOptions{},
					)

					checkErr(err, "Failed to create exec", 1)

					backupDir = "backups"
				}
			}
		}
	} else {
		log.Infof("Unknown content")
	}

	log.Infof("Creating duplicity container...")

	fullIfOlderThan := getVolumeLabel(vol, ".full_if_older_than")
	if fullIfOlderThan == "" {
		fullIfOlderThan = c.FullIfOlderThan
	}

	pathSeparator := "/"
	if strings.HasPrefix(c.DuplicityTargetURL, "swift://") {
		// Looks like I'm not the one to fall on this issue: http://stackoverflow.com/questions/27991960/upload-to-swift-pseudo-folders-using-duplicity
		pathSeparator = "_"
	}

	container, err := c.CreateContainer(
		docker.CreateContainerOptions{
			Config: &docker.Config{
				Cmd: []string{
					"--full-if-older-than", fullIfOlderThan,
					"--s3-use-new-style",
					"--no-encryption",
					"--allow-source-mismatch",
					vol.Mountpoint + "/" + backupDir,
					c.DuplicityTargetURL + pathSeparator + c.Hostname + pathSeparator + vol.Name,
				},
				Env: []string{
					"AWS_ACCESS_KEY_ID=" + c.AWSAccessKeyID,
					"AWS_SECRET_ACCESS_KEY=" + c.AWSSecretAccessKey,
					"SWIFT_USERNAME=" + c.SwiftUsername,
					"SWIFT_PASSWORD=" + c.SwiftPassword,
					"SWIFT_AUTHURL=" + c.SwiftAuthURL,
					"SWIFT_TENANTNAME=" + c.SwiftTenantName,
					"SWIFT_REGIONNAME=" + c.SwiftRegionName,
					"SWIFT_AUTHVERSION=2",
				},
				Image:        c.Image,
				OpenStdin:    true,
				StdinOnce:    true,
				AttachStdin:  true,
				AttachStdout: true,
				AttachStderr: true,
				Tty:          true,
			},
		},
	)

	checkErr(err, "Failed to create container for volume "+vol.Name+": %v", 1)

	defer func() {
		c.RemoveContainer(docker.RemoveContainerOptions{
			ID:    container.ID,
			Force: true,
		})
	}()

	binds := []string{
		vol.Name + ":" + vol.Mountpoint + ":ro",
	}

	err = dockerpty.Start(c.Client, container, &docker.HostConfig{
		Binds: binds,
	})
	checkErr(err, "Failed to start container for volume "+vol.Name+": %v", -1)
	return
  return nil
}


func getVolumeLabel(vol *docker.Volume, key string) (value string) {
	value = vol.Labels[labelPrefix+key]
	return
}

func checkErr(err error, msg string, exit int) {
	if err != nil {
		log.Errorf(msg, err)

		if exit != -1 {
			os.Exit(exit)
		}
	}
}