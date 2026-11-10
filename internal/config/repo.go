package config

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/verified-dispatch/verified-dispatch/internal/gitx"
)

// RepoFile is the repository configuration path, relative to the top level.
const RepoFile = ".vdispatch.toml"

// ReadRepoBlob reads RepoFile from the HEAD tree through r, never from
// disk. present is false when HEAD has no such entry. An entry that is not
// a regular blob (a symlink or a directory), or a blob over ConfigFileBytes,
// is an error.
func ReadRepoBlob(ctx context.Context, r gitx.Runner) (data []byte, present bool, err error) {
	out, err := r.Run(ctx, "ls-tree", "-z", "HEAD", "--", RepoFile)
	if err != nil {
		return nil, false, err
	}
	entries, err := gitx.ParseZ(out)
	if err != nil {
		return nil, false, err
	}
	if len(entries) == 0 {
		return nil, false, nil
	}
	// Each entry is "<mode> <type> <object>\t<path>".
	meta, _, _ := strings.Cut(entries[0], "\t")
	fields := strings.Fields(meta)
	if len(fields) < 3 {
		return nil, false, fmt.Errorf("config: unexpected ls-tree entry %q", entries[0])
	}
	mode, typ, oid := fields[0], fields[1], fields[2]
	if typ != "blob" {
		return nil, false, fmt.Errorf("config: %s in HEAD is a %s, not a file", RepoFile, typ)
	}
	if mode == "120000" {
		return nil, false, fmt.Errorf("config: %s in HEAD is a symbolic link, not a file", RepoFile)
	}
	r.MaxOutput = ConfigFileBytes + 1
	data, err = r.Run(ctx, "cat-file", "blob", oid)
	if errors.Is(err, gitx.ErrOutputTooLarge) {
		return nil, false, fmt.Errorf("config: %s in HEAD is larger than %d bytes", RepoFile, ConfigFileBytes)
	}
	if err != nil {
		return nil, false, err
	}
	if len(data) > ConfigFileBytes {
		return nil, false, fmt.Errorf("config: %s in HEAD is larger than %d bytes", RepoFile, ConfigFileBytes)
	}
	return data, true, nil
}
