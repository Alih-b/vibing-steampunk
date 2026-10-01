//go:build integration

package adt

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// A stateless request that another caller of the same client sends between
// LOCK and the write -- a second agent reading something -- must not end the
// context the lock handle belongs to.
func TestIntegration_StatelessRequestInsideAnotherCallersLockWindow(t *testing.T) {
	client := getIntegrationClient(t)
	ctx := context.Background()

	name := fmt.Sprintf("ZVSP_LR_%05d", time.Now().Unix()%100000)
	if err := client.CreateObject(ctx, CreateObjectOptions{
		ObjectType: ObjectTypeProgram, Name: name, Description: "vsp lock race probe", PackageName: "$TMP",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	objectURL := GetObjectURL(ObjectTypeProgram, name, "")
	sourceURL := GetSourceURL(ObjectTypeProgram, name, "")
	defer func() {
		lock, err := client.LockObject(ctx, objectURL, "MODIFY")
		if err != nil {
			t.Logf("cleanup lock: %v", err)
			return
		}
		if err := client.DeleteObject(ctx, objectURL, lock.LockHandle, ""); err != nil {
			t.Logf("cleanup delete: %v", err)
			_ = client.UnlockObject(ctx, objectURL, lock.LockHandle)
		}
	}()

	lock, err := client.LockObject(ctx, objectURL, "MODIFY")
	if err != nil {
		t.Fatalf("lock: %v", err)
	}

	// The other caller: an ordinary stateless read.
	if _, err := client.SearchObject(ctx, "ZVSP_LR_*", 5); err != nil {
		t.Logf("stateless read: %v", err)
	}

	err = client.UpdateSource(ctx, sourceURL, "REPORT "+name+".\nWRITE 'x'.", lock.LockHandle, "")
	_ = client.UnlockObject(ctx, objectURL, lock.LockHandle)
	if err != nil {
		t.Fatalf("the write after another caller's stateless read failed: %v", err)
	}
}

// Two agents editing through one client while a third keeps reading: every
// write has to land, whatever the interleaving.
func TestIntegration_ConcurrentLockChainsAndReaders(t *testing.T) {
	client := getIntegrationClient(t)
	ctx := context.Background()

	base := time.Now().Unix() % 10000
	names := []string{fmt.Sprintf("ZVSP_LRA_%04d", base), fmt.Sprintf("ZVSP_LRB_%04d", base)}
	for _, name := range names {
		if err := client.CreateObject(ctx, CreateObjectOptions{
			ObjectType: ObjectTypeProgram, Name: name, Description: "vsp lock race probe", PackageName: "$TMP",
		}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		name := name
		defer func() {
			u := GetObjectURL(ObjectTypeProgram, name, "")
			lock, err := client.LockObject(ctx, u, "MODIFY")
			if err != nil {
				t.Logf("cleanup lock %s: %v", name, err)
				return
			}
			if err := client.DeleteObject(ctx, u, lock.LockHandle, ""); err != nil {
				t.Logf("cleanup delete %s: %v", name, err)
				_ = client.UnlockObject(ctx, u, lock.LockHandle)
			}
		}()
	}

	stop := make(chan struct{})
	readerDone := make(chan int)
	go func() {
		n := 0
		for {
			select {
			case <-stop:
				readerDone <- n
				return
			default:
			}
			_, _ = client.SearchObject(ctx, "ZVSP_LR*", 5)
			n++
		}
	}()

	errs := make(chan error, 2*5)
	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			u := GetObjectURL(ObjectTypeProgram, name, "")
			for i := 0; i < 5; i++ {
				lock, err := client.LockObject(ctx, u, "MODIFY")
				if err != nil {
					errs <- fmt.Errorf("%s lock %d: %w", name, i, err)
					continue
				}
				src := fmt.Sprintf("REPORT %s.\nWRITE '%d'.", name, i)
				if err := client.UpdateSource(ctx, GetSourceURL(ObjectTypeProgram, name, ""), src, lock.LockHandle, ""); err != nil {
					errs <- fmt.Errorf("%s write %d: %w", name, i, err)
				}
				_ = client.UnlockObject(ctx, u, lock.LockHandle)
			}
		}(name)
	}
	wg.Wait()
	close(stop)
	t.Logf("reader requests during the chains: %d", <-readerDone)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
