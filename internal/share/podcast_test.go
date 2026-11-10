package share

import (
	"context"
	"testing"
)

// TestPodcastActions: every role against the podcast and radio actions (decision 0038).
func TestPodcastActions(t *testing.T) {
	f := openFixture(t)
	ctx := context.Background()
	f.exec(t, `INSERT INTO podcast_shows(id, feed_url, created_at, updated_at) VALUES ('ps_1', 'https://a.example/f.xml', 1, 1), ('ps_2', 'https://b.example/f.xml', 1, 1)`)
	f.exec(t, `INSERT INTO podcast_episodes(id, show_id, guid_key, title, audio_url, created_at, updated_at) VALUES
		('pe_1', 'ps_1', 'g1', 'One', 'https://a.example/1.mp3', 1, 1), ('pe_2', 'ps_2', 'g2', 'Two', 'https://b.example/2.mp3', 1, 1)`)
	// u_lib follows show 1; u_all has only a state row for episode 2 (an unfollowed show's history).
	f.exec(t, `INSERT INTO podcast_subscriptions(user_id, show_id, followed_at, seen_at) VALUES ('u_lib', 'ps_1', 1, 1), ('u_own', 'ps_1', 1, 1), ('u_g', 'ps_1', 1, 1), ('u_dis', 'ps_1', 1, 1)`)
	f.exec(t, `INSERT INTO podcast_episode_state(user_id, episode_id, updated_at) VALUES ('u_all', 'pe_2', 1)`)

	server := Target{Kind: TargetServer}
	ep := func(id string) Target { return Target{Kind: TargetEpisode, ID: id} }
	cases := []struct {
		name   string
		user   string
		action Action
		target Target
		want   bool
	}{
		{"owner uses podcasts", "u_own", UsePodcasts, server, true},
		{"admin uses podcasts", "u_adm", UsePodcasts, server, true},
		{"member uses podcasts", "u_none", UsePodcasts, server, true},
		{"contributor uses podcasts", "u_con", UsePodcasts, server, true},
		{"guest refused podcasts", "u_g", UsePodcasts, server, false},
		{"disabled refused podcasts", "u_dis", UsePodcasts, server, false},
		{"podcasts need the server target", "u_own", UsePodcasts, Target{Kind: TargetBook, ID: "bk_a1"}, false},
		{"owner uses radio", "u_own", UseRadio, server, true},
		{"member uses radio", "u_none", UseRadio, server, true},
		{"guest refused radio", "u_g", UseRadio, server, false},
		{"disabled refused radio", "u_dis", UseRadio, server, false},
		{"follower listens", "u_lib", ListenEpisode, ep("pe_1"), true},
		{"owner following listens", "u_own", ListenEpisode, ep("pe_1"), true},
		{"follower of the other show refused", "u_lib", ListenEpisode, ep("pe_2"), false},
		{"state row alone listens", "u_all", ListenEpisode, ep("pe_2"), true},
		{"state row is not the show", "u_all", ListenEpisode, ep("pe_1"), false},
		{"stranger refused", "u_none", ListenEpisode, ep("pe_1"), false},
		{"admin who does not follow refused", "u_adm", ListenEpisode, ep("pe_1"), false},
		{"guest who follows refused", "u_g", ListenEpisode, ep("pe_1"), false},
		{"disabled follower refused", "u_dis", ListenEpisode, ep("pe_1"), false},
		{"contributor who does not follow refused", "u_con", ListenEpisode, ep("pe_1"), false},
		{"unknown episode refused", "u_lib", ListenEpisode, ep("pe_x"), false},
		{"empty episode id refused", "u_lib", ListenEpisode, ep(""), false},
		{"wrong target kind refused", "u_lib", ListenEpisode, Target{Kind: TargetBook, ID: "pe_1"}, false},
		{"library grant gives no episode", "u_all", ListenEpisode, ep("pe_1"), false},
	}
	for _, c := range cases {
		if got := f.c.Can(ctx, f.user(c.user), c.action, c.target); got != c.want {
			t.Errorf("%s: Can = %v, want %v", c.name, got, c.want)
		}
	}
}
