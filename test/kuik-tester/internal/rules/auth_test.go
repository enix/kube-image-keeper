package rules

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

func privateState() *state.State {
	return &state.State{
		Users: []api.User{{Name: userAlice, Password: "pw"}, {Name: userBob, Password: "pw"}},
		Grants: []api.Grant{{
			Host: privateHost, Repo: "team/**",
			Actions: []api.Action{api.ActionPull}, Users: []string{userAlice},
		}},
	}
}

var _ = Describe("Authorize", func() {
	Context("with a grant on a private host", func() {
		DescribeTable("decides per caller, repo, action and host",
			func(host, repo string, a api.Action, user string, want Decision) {
				Expect(Authorize(privateState(), host, repo, a, user)).To(Equal(want))
			},
			Entry("challenges an anonymous caller", privateHost, repoTeamApp, api.ActionPull, "", Challenge),
			Entry("denies a user who is not granted", privateHost, repoTeamApp, api.ActionPull, userBob, Deny),
			Entry("allows a granted user", privateHost, repoTeamApp, api.ActionPull, userAlice, Allow),
			Entry("allows a repo no grant covers", privateHost, "public/x", api.ActionPull, "", Allow),
			Entry("allows an action no grant covers", privateHost, repoTeamApp, api.ActionPush, userAlice, Allow),
			Entry("allows another host", upstreamHost, repoTeamApp, api.ActionPull, "", Allow),
		)
	})

	Context("with several grants on the same host", func() {
		var st *state.State

		BeforeEach(func() {
			st = &state.State{
				Users: []api.User{{Name: userManage, Password: "pw"}, {Name: userPull, Password: "pw"}, {Name: "carol", Password: "pw"}},
				Grants: []api.Grant{
					{
						Host: mirrorHost, Repo: "**",
						Actions: []api.Action{api.ActionPush, api.ActionDelete, api.ActionPull},
						Users:   []string{userManage},
					},
					{
						Host: mirrorHost, Repo: "**",
						Actions: []api.Action{api.ActionPull}, Users: []string{userPull},
					},
				},
			}
		})

		DescribeTable("takes the union of the grants",
			func(user string, a api.Action, want Decision) {
				Expect(Authorize(st, mirrorHost, "app", a, user)).To(Equal(want))
			},
			Entry("allows pull by the pull user", userPull, api.ActionPull, Allow),
			Entry("allows pull by the manage user", userManage, api.ActionPull, Allow),
			Entry("denies push by the pull user", userPull, api.ActionPush, Deny),
			Entry("allows push by the manage user", userManage, api.ActionPush, Allow),
			Entry("denies pull by another user", "carol", api.ActionPull, Deny),
			Entry("challenges an anonymous pull", "", api.ActionPull, Challenge),
		)
	})
})

var _ = Describe("ActionFor", func() {
	DescribeTable("maps a request kind and method to an action",
		func(kind api.Kind, method string, want api.Action) {
			Expect(ActionFor(kind, method)).To(Equal(want), "ActionFor(%s, %s)", kind, method)
		},
		Entry("GET of a manifest by tag is a pull", api.KindManifestByTag, http.MethodGet, api.ActionPull),
		Entry("HEAD of a manifest by tag is a pull", api.KindManifestByTag, http.MethodHead, api.ActionPull),
		Entry("GET of a tags list is a pull", api.KindTagsList, http.MethodGet, api.ActionPull),
		Entry("POST of an upload is a push", api.KindUpload, "POST", api.ActionPush),
		Entry("PATCH of an upload is a push", api.KindUpload, "PATCH", api.ActionPush),
		Entry("PUT of an upload is a push", api.KindUpload, "PUT", api.ActionPush),
		Entry("PUT of a manifest is a push", api.KindManifestPut, "PUT", api.ActionPush),
		Entry("DELETE is a delete", api.KindDelete, "DELETE", api.ActionDelete),
	)
})

var _ = Describe("Challenges", func() {
	It("does not challenge a host without any grant", func() {
		Expect(Challenges(&state.State{}, privateHost)).To(BeFalse())
	})

	It("challenges a host a grant pattern covers, and only that one", func() {
		st := &state.State{Grants: []api.Grant{{Host: "priv*"}}}
		Expect(Challenges(st, privateHost)).To(BeTrue())
		Expect(Challenges(st, upstreamHost)).To(BeFalse())
	})

	It("challenges a bearer host", func() {
		st := &state.State{HostAuth: []api.HostAuth{{Host: "bear", Scheme: schemeBearer}}}
		Expect(Challenges(st, "bear")).To(BeTrue())
	})
})

var _ = Describe("CheckPassword", func() {
	It("accepts the right password, rejects a wrong one and an unknown user", func() {
		st := privateState()
		Expect(CheckPassword(st, userAlice, "pw")).To(BeTrue(), "right password")
		Expect(CheckPassword(st, userAlice, "nope")).To(BeFalse(), "wrong password")
		Expect(CheckPassword(st, "carol", "pw")).To(BeFalse(), "unknown user")
	})
})

var _ = Describe("Scheme", func() {
	It("returns the scheme configured for a host and basic otherwise", func() {
		st := &state.State{HostAuth: []api.HostAuth{{Host: "bear", Scheme: schemeBearer}}}
		Expect(Scheme(st, "bear")).To(Equal(api.SchemeBearer))
		Expect(Scheme(st, "other")).To(Equal(api.SchemeBasic))
	})
})
