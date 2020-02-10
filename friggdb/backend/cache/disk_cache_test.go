package cache

import (
	"io/ioutil"
	"os"
	"path"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestReadOrCache(t *testing.T) {
	tempDir, err := ioutil.TempDir("/tmp", "")
	defer os.RemoveAll(tempDir)
	assert.NoError(t, err, "unexpected error creating temp dir")

	missBytes := []byte{0x01}
	missCalled := 0
	missFunc := func(blockID uuid.UUID, tenantID string) ([]byte, error) {
		missCalled++
		return missBytes, nil
	}

	cache, err := New(nil, &Config{
		Path:           tempDir,
		MaxDiskMBs:     1024,
		DiskPruneCount: 10,
		DiskCleanRate:  time.Hour,
	})
	assert.NoError(t, err)

	blockID := uuid.New()
	tenantID := "fake"

	bytes, err := cache.(*reader).readOrCacheKeyToDisk(blockID, tenantID, "type", missFunc)
	assert.NoError(t, err)
	assert.Equal(t, missBytes, bytes)
	assert.Equal(t, 1, missCalled)

	bytes, err = cache.(*reader).readOrCacheKeyToDisk(blockID, tenantID, "type", missFunc)
	assert.NoError(t, err)
	assert.Equal(t, missBytes, bytes)
	assert.Equal(t, 1, missCalled)
}

func TestJanitor(t *testing.T) {
	tempDir, err := ioutil.TempDir("/tmp", "")
	defer os.RemoveAll(tempDir)
	assert.NoError(t, err, "unexpected error creating temp dir")

	// 1KB per file
	missBytes := make([]byte, 1024, 1024)
	missFunc := func(blockID uuid.UUID, tenantID string) ([]byte, error) {
		return missBytes, nil
	}

	cache, err := New(nil, &Config{
		Path:           tempDir,
		MaxDiskMBs:     30,
		DiskPruneCount: 10,
		DiskCleanRate:  time.Hour,
	})
	assert.NoError(t, err)

	// test
	for i := 0; i < 10; i++ {
		blockID := uuid.New()
		tenantID := "fake"

		bytes, err := cache.(*reader).readOrCacheKeyToDisk(blockID, tenantID, "type", missFunc)
		assert.NoError(t, err)
		assert.Equal(t, missBytes, bytes)
	}

	// force prune. should do nothing b/c we don't have enough files
	cleaned := clean(tempDir, 1, 10)
	assert.False(t, cleaned)

	// now make enough files to prune
	fi, err := ioutil.ReadDir(tempDir)
	assert.Len(t, fi, 10)
	assert.NoError(t, err)

	// create 1024 files
	for i := 0; i < 1024; i++ {
		blockID := uuid.New()
		tenantID := "fake"

		bytes, err := cache.(*reader).readOrCacheKeyToDisk(blockID, tenantID, "type", missFunc)
		assert.NoError(t, err)
		assert.Equal(t, missBytes, bytes)
	}

	fi, err = ioutil.ReadDir(tempDir)
	assert.Len(t, fi, 1034)
	assert.NoError(t, err)

	// force clean at 1MB and see only 1033 (b/c prunecount = 1)
	cleaned = clean(tempDir, 1, 1)
	assert.True(t, cleaned)

	fi, err = ioutil.ReadDir(tempDir)
	assert.Len(t, fi, 1033)
	assert.NoError(t, err)

	// force clean at 1MB and see only 1023 (b/c prunecount = 10)
	cleaned = clean(tempDir, 1, 10)
	assert.True(t, cleaned)

	fi, err = ioutil.ReadDir(tempDir)
	assert.Len(t, fi, 1023)
	assert.NoError(t, err)

	// force clean at 1MB and see only 1023 (b/c we're less than 1MB)
	cleaned = clean(tempDir, 1, 10)
	assert.False(t, cleaned)

	fi, err = ioutil.ReadDir(tempDir)
	assert.Len(t, fi, 1023)
	assert.NoError(t, err)
}

func TestJanitorCleanupOrder(t *testing.T) {
	tempDir, err := ioutil.TempDir("/tmp", "")
	defer os.RemoveAll(tempDir)
	assert.NoError(t, err, "unexpected error creating temp dir")

	// 1MB per file
	missCalled := 0
	missBytes := make([]byte, 1024*1024, 1024*1024)
	missFunc := func(blockID uuid.UUID, tenantID string) ([]byte, error) {
		missCalled++
		return missBytes, nil
	}

	cache, err := New(nil, &Config{
		Path:           tempDir,
		MaxDiskMBs:     30,
		DiskPruneCount: 10,
		DiskCleanRate:  time.Hour,
	})
	assert.NoError(t, err)

	// add 3 files
	tenantID := "fake"
	firstBlockID, _ := uuid.FromBytes([]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	secondBlockID, _ := uuid.FromBytes([]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01})
	thirdBlockID, _ := uuid.FromBytes([]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02})

	bytes, err := cache.(*reader).readOrCacheKeyToDisk(firstBlockID, tenantID, "type", missFunc)
	assert.NoError(t, err)
	assert.Equal(t, missBytes, bytes)
	assert.Equal(t, 1, missCalled)

	bytes, err = cache.(*reader).readOrCacheKeyToDisk(secondBlockID, tenantID, "type", missFunc)
	assert.NoError(t, err)
	assert.Equal(t, missBytes, bytes)
	assert.Equal(t, 2, missCalled)

	bytes, err = cache.(*reader).readOrCacheKeyToDisk(thirdBlockID, tenantID, "type", missFunc)
	assert.NoError(t, err)
	assert.Equal(t, missBytes, bytes)
	assert.Equal(t, 3, missCalled)

	fi, err := ioutil.ReadDir(tempDir)
	assert.Len(t, fi, 3)
	assert.NoError(t, err)

	time.Sleep(time.Second) // sadface, smaller values seem to make this test flaky

	_, err = ioutil.ReadFile(path.Join(tempDir, fi[1].Name()))
	assert.NoError(t, err)

	var newestFi os.FileInfo
	for _, info := range fi {
		if newestFi == nil {
			newestFi = info
			continue
		}

		if info.Sys().(*syscall.Stat_t).Atim.Nano() > newestFi.Sys().(*syscall.Stat_t).Atim.Nano() {
			newestFi = info
		}
	}

	// force prune. should prune 2
	cleaned := clean(tempDir, 1, 2)
	assert.True(t, cleaned)

	fi, err = ioutil.ReadDir(tempDir)
	assert.Len(t, fi, 1)
	assert.NoError(t, err)

	// confirm the third block is still in cache as it was created last
	assert.Equal(t, newestFi.Name(), fi[0].Name())
}
