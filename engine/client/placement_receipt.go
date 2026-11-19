package client

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var errInvalidCheckoutReceipt = errors.New("invalid native checkout assignment")

type checkoutReceipt struct {
	root *os.Root
	key  string
}
type checkoutAssignment struct {
	Version int    `json:"version"`
	Worker  string `json:"worker"`
}

func checkoutPlacementKey(endpoint, checkout string) string {
	if endpoint == "" || checkout == "" {
		return ""
	}
	key := sha256.Sum256([]byte(endpoint + "\x00" + checkout))
	return hex.EncodeToString(key[:])
}
func openCheckoutReceipt(parent, key string) (*checkoutReceipt, error) {
	if !filepath.IsAbs(parent) {
		return nil, fmt.Errorf("%w: state directory must be absolute", errInvalidCheckoutReceipt)
	}
	if len(key) != 64 {
		return nil, errInvalidCheckoutReceipt
	}
	if _, err := hex.DecodeString(key); err != nil {
		return nil, errInvalidCheckoutReceipt
	}
	if err := os.MkdirAll(parent, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return nil, err
	}
	return &checkoutReceipt{root: root, key: key}, nil
}
func (r *checkoutReceipt) Close() error { return r.root.Close() }
func validPlacementWorker(worker string) bool {
	return worker != "" && len(worker) <= 256 && !strings.ContainsAny(worker, "\x00\r\n")
}
func (r *checkoutReceipt) read() (string, error) {
	file, err := r.root.Open(r.key)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 2049))
	if err != nil {
		return "", err
	}
	if len(data) > 2048 {
		return "", errInvalidCheckoutReceipt
	}
	var assignment checkoutAssignment
	if err := json.Unmarshal(data, &assignment); err != nil {
		return "", fmt.Errorf("%w: %v", errInvalidCheckoutReceipt, err)
	}
	if assignment.Version != 1 || !validPlacementWorker(assignment.Worker) {
		return "", errInvalidCheckoutReceipt
	}
	return assignment.Worker, nil
}
func (r *checkoutReceipt) remember(worker string) error {
	if !validPlacementWorker(worker) {
		return errInvalidCheckoutReceipt
	}
	data, err := json.Marshal(checkoutAssignment{Version: 1, Worker: worker})
	if err != nil {
		return err
	}
	name := r.key + "." + rand.Text() + ".tmp"
	file, err := r.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer r.root.Remove(name)
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return r.root.Rename(name, r.key)
}
