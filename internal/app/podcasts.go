package app

import (
	"context"

	"github.com/Tend-Stack/tendshelf/internal/metadata/online/podnet"
	"github.com/Tend-Stack/tendshelf/internal/podcast"
)

// registerPodcasts builds the podcast service (decision 0038): the guarded client, the store, the five
// job kinds and the cache folders under the data directory. Nothing runs until the owner turns podcasts on:
// every job checks the setting first and the scheduler sleeps. The scheduler itself is started by Run.
func registerPodcasts(d deps) (*podcast.Service, error) {
	svc := podcast.New(d.st, podnet.New(podnet.Options{}), d.runner, d.dataDir, nil, d.log)
	svc.Thumb = d.tools.Thumbnail
	if d.tools.FFprobe != "" {
		svc.ProbeChapters = func(ctx context.Context, path string) ([]podcast.Chapter, error) {
			p, err := d.tools.Probe(ctx, path) // the ffprobe wrapper: no network protocols, a deadline, output limits, DRM refusal
			if err != nil {
				return nil, err
			}
			out := make([]podcast.Chapter, 0, len(p.Chapters))
			for _, c := range p.Chapters {
				out = append(out, podcast.Chapter{StartMs: c.StartMs, Title: c.Title})
			}
			return out, nil
		}
	}
	if err := svc.Register(); err != nil {
		return nil, err
	}
	return svc, nil
}
