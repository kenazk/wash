package docker

import (
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/puppetlabs/wash/activity"
	"github.com/puppetlabs/wash/plugin"
)

type container struct {
	plugin.EntryBase
	id     string
	client *client.Client
}

func (c *container) Metadata(ctx context.Context) (plugin.EntryMetadata, error) {
	// Use raw to also get the container size.
	_, raw, err := c.client.ContainerInspectWithRaw(ctx, c.id, true)
	if err != nil {
		return nil, err
	}

	return plugin.ToMeta(raw), nil
}

func (c *container) List(ctx context.Context) ([]plugin.Entry, error) {
	// TODO: May be worth creating a helper that makes it easy to create
	// read-only files. Lots of shared code between these two.
	cm := &containerMetadata{plugin.NewEntry("metadata.json"), c}
	cm.DisableDefaultCaching()
	content, err := cm.Open(ctx)
	if err != nil {
		return nil, err
	}
	cmAttr := plugin.EntryAttributes{}
	cmAttr.SetSize(uint64(content.Size()))
	cm.SetAttributes(cmAttr)

	clf := &containerLogFile{plugin.NewEntry("log"), c.id, c.client}
	clf.DisableCachingFor(plugin.MetadataOp)
	content, err = clf.Open(ctx)
	if err != nil {
		return nil, err
	}
	clfAttr := plugin.EntryAttributes{}
	clfAttr.SetSize(uint64(content.Size()))
	clf.SetAttributes(clfAttr)

	return []plugin.Entry{cm, clf}, nil
}

func (c *container) Exec(ctx context.Context, cmd string, args []string, opts plugin.ExecOptions) (plugin.ExecResult, error) {
	execResult := plugin.ExecResult{}

	command := append([]string{cmd}, args...)
	cfg := types.ExecConfig{Cmd: command, AttachStdout: true, AttachStderr: true}
	if opts.Stdin != nil {
		cfg.AttachStdin = true
	}
	created, err := c.client.ContainerExecCreate(ctx, c.id, cfg)
	if err != nil {
		return execResult, err
	}

	resp, err := c.client.ContainerExecAttach(ctx, created.ID, types.ExecStartCheck{})
	if err != nil {
		return execResult, err
	}

	outputCh, stdout, stderr := plugin.CreateExecOutputStreams(ctx)
	go func() {
		defer resp.Close()

		_, err := stdcopy.StdCopy(stdout, stderr, resp.Reader)
		activity.Record(ctx, "Exec on %v complete: %v", c.Name(), err)
		stdout.CloseWithError(err)
		stderr.CloseWithError(err)
	}()

	var writeErr error
	if opts.Stdin != nil {
		go func() {
			_, writeErr = io.Copy(resp.Conn, opts.Stdin)
			respErr := resp.CloseWrite()
			activity.Record(ctx, "Closed execution input stream for %v: %v, %v", c.Name(), writeErr, respErr)
		}()
	}

	execResult.OutputCh = outputCh
	execResult.ExitCodeCB = func() (int, error) {
		if writeErr != nil {
			return 0, err
		}

		resp, err := c.client.ContainerExecInspect(ctx, created.ID)
		if err != nil {
			return 0, err
		}

		if resp.Running {
			return 0, fmt.Errorf("the command was marked as 'Running' even though the output streams reached EOF")
		}

		activity.Record(ctx, "Exec on %v exited %v", c.Name(), resp.ExitCode)
		return resp.ExitCode, nil
	}

	return execResult, nil
}
