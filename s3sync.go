// Copyright 2019 SEQSENSE, Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package s3sync

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3iface"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
)

// Manager manages the sync operation.
type Manager struct {
	s3 s3iface.S3API
}

// Option is the option of s3sync behavior.
type Option struct {
}

type s3Path struct {
	bucket       string
	bucketPrefix string
}

type fileInfo struct {
	err          error
	name         string
	path         string
	size         int64
	lastModified time.Time
}

func urlToS3Path(url *url.URL) (*s3Path, error) {
	if url.Host == "" {
		return nil, errors.New("s3 url is missing bucket name")
	}

	return &s3Path{
		bucket:       url.Host,
		bucketPrefix: strings.TrimPrefix(url.Path, "/"),
	}, nil
}

// New returns a new Manager.
func New(sess *session.Session) *Manager {
	return NewWithOption(sess, &Option{})
}

// NewWithOption returns a new Manager with the given option.
func NewWithOption(sess *session.Session, option *Option) *Manager {
	return &Manager{
		s3: s3.New(sess),
	}
}

// Sync syncs the files between s3 and local disks.
func (m *Manager) Sync(source, dest string) error {
	sourceURL, err := url.Parse(source)
	if err != nil {
		return err
	}

	destURL, err := url.Parse(dest)
	if err != nil {
		return err
	}

	if isS3URL(sourceURL) {
		sourceS3Path, err := urlToS3Path(sourceURL)
		if err != nil {
			return err
		}
		if isS3URL(destURL) {
			destS3Path, err := urlToS3Path(destURL)
			if err != nil {
				return err
			}
			return m.syncS3ToS3(sourceS3Path, destS3Path)
		}
		return m.syncS3ToLocal(sourceS3Path, dest)
	}

	if isS3URL(destURL) {
		destS3Path, err := urlToS3Path(destURL)
		if err != nil {
			return err
		}
		return m.syncLocalToS3(source, destS3Path)
	}

	return errors.New("local to local sync is not supported")
}

func isS3URL(url *url.URL) bool {
	return url.Scheme == "s3"
}

func (m *Manager) syncS3ToS3(sourcePath, destPath *s3Path) error {
	return errors.New("S3 to S3 sync feature is not implemented")
}

func (m *Manager) syncLocalToS3(sourcePath string, destPath *s3Path) error {
	return errors.New("Local to S3 sync feature is not implemented")
}

// syncS3ToLocal syncs the given s3 path to the given local path.
func (m *Manager) syncS3ToLocal(sourcePath *s3Path, destPath string) error {
	wg := &sync.WaitGroup{}
	mutex := sync.Mutex{}
	errMsgs := []string{}
	for source := range filterFilesForSync(m.listS3Files(sourcePath), listLocalFiles(destPath)) {
		wg.Add(1)
		go func(source *fileInfo) {
			defer wg.Done()
			if source.err != nil {
				mutex.Lock()
				errMsgs = append(errMsgs, source.err.Error())
				mutex.Unlock()
				return
			}
			err := m.download(source, sourcePath, destPath)

			if err != nil {
				mutex.Lock()
				errMsgs = append(errMsgs, err.Error())
				mutex.Unlock()
			}
		}(source)
	}
	wg.Wait()

	if len(errMsgs) > 0 {
		return errors.New(strings.Join(errMsgs, "\n"))
	}
	return nil
}

func (m *Manager) download(file *fileInfo, sourcePath *s3Path, destPath string) error {
	targetFilename := filepath.Join(destPath, file.name)
	targetDir := filepath.Dir(targetFilename)

	println("Downloading", file.name, "to", targetFilename)

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}

	writer, err := os.Create(targetFilename)

	if err != nil {
		return err
	}

	defer writer.Close()

	_, err = s3manager.NewDownloaderWithClient(m.s3).Download(writer, &s3.GetObjectInput{
		Bucket: aws.String(sourcePath.bucket),
		Key:    aws.String(filepath.Join(sourcePath.bucketPrefix, file.name)),
	})

	if err != nil {
		return err
	}

	return nil
}

// listS3Files return a channel which receives the file infos under the given s3Path.
func (m *Manager) listS3Files(path *s3Path) chan *fileInfo {
	c := make(chan *fileInfo, 50000) // TODO: revisit this buffer size later

	go func() {
		defer close(c)
		var token *string
		for {
			if token = m.listS3FileWithToken(c, path, token); token == nil {
				break
			}
		}
	}()

	return c
}

// listS3FileWithToken lists (send to the result channel) the s3 files from the given continuation token.
func (m *Manager) listS3FileWithToken(c chan *fileInfo, path *s3Path, token *string) *string {
	list, err := m.s3.ListObjectsV2(&s3.ListObjectsV2Input{
		Bucket:            &path.bucket,
		Prefix:            &path.bucketPrefix,
		ContinuationToken: token,
	})
	if err != nil {
		sendErrorInfoToChannel(c, err)
		return nil
	}

	for _, object := range list.Contents {
		name, err := filepath.Rel(path.bucketPrefix, *object.Key)
		if err != nil {
			sendErrorInfoToChannel(c, err)
			continue
		}
		c <- &fileInfo{
			name:         name,
			path:         *object.Key,
			size:         *object.Size,
			lastModified: *object.LastModified,
		}
	}

	return list.NextContinuationToken
}

// listLocalFiles returns a channel which receives the infos of the files under the given basePath.
// basePath have to be absolute path.
func listLocalFiles(basePath string) chan *fileInfo {
	c := make(chan *fileInfo)

	basePath = filepath.ToSlash(basePath)

	go func() {
		defer close(c)

		stat, err := os.Stat(basePath)
		if os.IsNotExist(err) {
			// The path doesn't exist.
			// Returns and closes the channel without sending any.
			return
		} else if err != nil {
			sendErrorInfoToChannel(c, err)
			return
		}
		sendFileInfoToChannel(c, basePath, basePath, stat)

		if !stat.IsDir() {
			return
		}

		err = filepath.Walk(basePath, func(path string, stat os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			sendFileInfoToChannel(c, basePath, path, stat)
			return nil
		})

		if err != nil {
			sendErrorInfoToChannel(c, err)
		}

	}()
	return c
}

func sendFileInfoToChannel(c chan *fileInfo, basePath, path string, stat os.FileInfo) {
	if stat == nil || stat.IsDir() {
		return
	}
	relPath, _ := filepath.Rel(basePath, path)
	c <- &fileInfo{
		name:         relPath,
		path:         path,
		size:         stat.Size(),
		lastModified: stat.ModTime(),
	}
}

func sendErrorInfoToChannel(c chan *fileInfo, err error) {
	c <- &fileInfo{
		err: err,
	}
}

// filterFilesForSync filters the source files from the given destination files, and returns
// another channel which includes the files necessary to be synced.
func filterFilesForSync(sourceFileChan, destFileChan chan *fileInfo) chan *fileInfo {
	c := make(chan *fileInfo)

	destFiles, err := fileInfoChanToMap(destFileChan)

	go func() {
		defer close(c)
		if err != nil {
			sendErrorInfoToChannel(c, err)
			return
		}
		for sourceInfo := range sourceFileChan {
			destInfo, ok := destFiles[sourceInfo.name]
			// source is necessary to sync if
			// 1. The dest doesn't exist
			// 2. The dest doesn't have the same size as the source
			// 3. The dest is older than the source
			if !ok || sourceInfo.size != destInfo.size || sourceInfo.lastModified.After(destInfo.lastModified) {
				c <- sourceInfo
			}
		}
	}()

	return c
}

// fileInfoChanToMap accumulates the fileInfos from the given channel and returns a map.
// It retruns an error if the channel contains an error.
func fileInfoChanToMap(files chan *fileInfo) (map[string]*fileInfo, error) {
	result := make(map[string]*fileInfo)

	for file := range files {
		if file.err != nil {
			return nil, file.err
		}
		result[file.name] = file
	}
	return result, nil
}
