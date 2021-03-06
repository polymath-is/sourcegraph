package graphqlbackend

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sourcegraph/sourcegraph/cmd/frontend/types"
	"github.com/sourcegraph/sourcegraph/internal/search"
	searchbackend "github.com/sourcegraph/sourcegraph/internal/search/backend"
	"github.com/sourcegraph/sourcegraph/internal/search/query"
)

func TestSearchRepositories(t *testing.T) {
	repositories := []*search.RepositoryRevisions{
		{Repo: &types.Repo{ID: 123, Name: "foo/one"}, Revs: []search.RevisionSpecifier{{RevSpec: ""}}},
		{Repo: &types.Repo{ID: 456, Name: "foo/no-match"}, Revs: []search.RevisionSpecifier{{RevSpec: ""}}},
		{Repo: &types.Repo{ID: 789, Name: "bar/one"}, Revs: []search.RevisionSpecifier{{RevSpec: ""}}},
	}

	zoekt := &searchbackend.Zoekt{Client: &fakeSearcher{}}

	mockSearchFilesInRepos = func(args *search.TextParameters) (matches []*FileMatchResolver, common *searchResultsCommon, err error) {
		repoName := args.Repos[0].Repo.Name
		switch repoName {
		case "foo/one":
			return []*FileMatchResolver{
				{
					uri:  "git://" + string(repoName) + "?1a2b3c#" + "f.go",
					Repo: &RepositoryResolver{repo: &types.Repo{ID: 123}},
				},
			}, &searchResultsCommon{}, nil
		case "bar/one":
			return []*FileMatchResolver{
				{
					uri:  "git://" + string(repoName) + "?1a2b3c#" + "f.go",
					Repo: &RepositoryResolver{repo: &types.Repo{ID: 789}},
				},
			}, &searchResultsCommon{}, nil
		case "foo/no-match":
			return []*FileMatchResolver{}, &searchResultsCommon{}, nil
		default:
			return nil, &searchResultsCommon{}, errors.New("Unexpected repo")
		}
	}

	cases := []struct {
		name string
		q    string
		want []string
	}{{
		name: "all",
		q:    "type:repo",
		want: []string{"bar/one", "foo/no-match", "foo/one"},
	}, {
		name: "pattern filter",
		q:    "type:repo foo/one",
		want: []string{"foo/one"},
	}, {
		name: "repohasfile",
		q:    "foo type:repo repohasfile:f.go",
		want: []string{"foo/one"},
	}, {
		name: "case yes match",
		q:    "foo case:yes",
		want: []string{"foo/no-match", "foo/one"},
	}, {
		name: "case no match",
		q:    "Foo case:no",
		want: []string{"foo/no-match", "foo/one"},
	}, {
		name: "case exclude all",
		q:    "Foo case:yes",
		want: []string{},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := query.ParseAndCheck(tc.q)
			if err != nil {
				t.Fatal(err)
			}

			pattern, err := getPatternInfo(q, &getPatternInfoOptions{fileMatchLimit: 1})
			if err != nil {
				t.Fatal(err)
			}

			results, _, err := searchRepositories(context.Background(), &search.TextParameters{
				PatternInfo: pattern,
				Repos:       repositories,
				Query:       q,
				Zoekt:       zoekt,
			}, int32(100))
			if err != nil {
				t.Fatal(err)
			}

			var got []string
			for _, res := range results {
				r, ok := res.ToRepository()
				if !ok {
					t.Fatal("expected repo result")
				}
				got = append(got, string(r.repo.Name))
			}
			sort.Strings(got)

			if !cmp.Equal(tc.want, got, cmpopts.EquateEmpty()) {
				t.Errorf("mismatch (-want +got):\n%s", cmp.Diff(tc.want, got))
			}
		})
	}
}

func TestRepoShouldBeAdded(t *testing.T) {
	mockSearchFilesInRepos = func(args *search.TextParameters) (matches []*FileMatchResolver, common *searchResultsCommon, err error) {
		repoName := args.Repos[0].Repo.Name
		switch repoName {
		case "foo/one":
			return []*FileMatchResolver{
				{
					uri:  "git://" + string(repoName) + "?1a2b3c#" + "foo.go",
					Repo: &RepositoryResolver{repo: &types.Repo{ID: 123}},
				},
			}, &searchResultsCommon{}, nil
		case "foo/no-match":
			return []*FileMatchResolver{}, &searchResultsCommon{}, nil
		default:
			return nil, &searchResultsCommon{}, errors.New("Unexpected repo")
		}
	}

	zoekt := &searchbackend.Zoekt{Client: &fakeSearcher{}}

	t.Run("repo should be included in results, query has repoHasFile filter", func(t *testing.T) {
		repo := &search.RepositoryRevisions{Repo: &types.Repo{ID: 123, Name: "foo/one"}, Revs: []search.RevisionSpecifier{{RevSpec: ""}}}
		mockSearchFilesInRepos = func(args *search.TextParameters) (matches []*FileMatchResolver, common *searchResultsCommon, err error) {
			return []*FileMatchResolver{
				{
					uri:  "git://" + string(repo.Repo.Name) + "?1a2b3c#" + "foo.go",
					Repo: &RepositoryResolver{repo: &types.Repo{ID: 123}},
				},
			}, &searchResultsCommon{}, nil
		}
		pat := &search.TextPatternInfo{Pattern: "", FilePatternsReposMustInclude: []string{"foo"}, IsRegExp: true, FileMatchLimit: 1, PathPatternsAreCaseSensitive: false, PatternMatchesContent: true, PatternMatchesPath: true}
		shouldBeAdded, err := repoShouldBeAdded(context.Background(), zoekt, repo, pat)
		if err != nil {
			t.Fatal(err)
		}
		if !shouldBeAdded {
			t.Errorf("Expected shouldBeAdded for repo %v to be true, but got false", repo)
		}
	})

	t.Run("repo shouldn't be included in results, query has repoHasFile filter ", func(t *testing.T) {
		repo := &search.RepositoryRevisions{Repo: &types.Repo{Name: "foo/no-match"}, Revs: []search.RevisionSpecifier{{RevSpec: ""}}}
		mockSearchFilesInRepos = func(args *search.TextParameters) (matches []*FileMatchResolver, common *searchResultsCommon, err error) {
			return []*FileMatchResolver{}, &searchResultsCommon{}, nil
		}
		pat := &search.TextPatternInfo{Pattern: "", FilePatternsReposMustInclude: []string{"foo"}, IsRegExp: true, FileMatchLimit: 1, PathPatternsAreCaseSensitive: false, PatternMatchesContent: true, PatternMatchesPath: true}
		shouldBeAdded, err := repoShouldBeAdded(context.Background(), zoekt, repo, pat)
		if err != nil {
			t.Fatal(err)
		}
		if shouldBeAdded {
			t.Errorf("Expected shouldBeAdded for repo %v to be false, but got true", repo)
		}
	})

	t.Run("repo shouldn't be included in results, query has -repoHasFile filter", func(t *testing.T) {
		repo := &search.RepositoryRevisions{Repo: &types.Repo{ID: 123, Name: "foo/one"}, Revs: []search.RevisionSpecifier{{RevSpec: ""}}}
		mockSearchFilesInRepos = func(args *search.TextParameters) (matches []*FileMatchResolver, common *searchResultsCommon, err error) {
			return []*FileMatchResolver{
				{
					uri:  "git://" + string(repo.Repo.Name) + "?1a2b3c#" + "foo.go",
					Repo: &RepositoryResolver{repo: &types.Repo{ID: 123}},
				},
			}, &searchResultsCommon{}, nil
		}
		pat := &search.TextPatternInfo{Pattern: "", FilePatternsReposMustExclude: []string{"foo"}, IsRegExp: true, FileMatchLimit: 1, PathPatternsAreCaseSensitive: false, PatternMatchesContent: true, PatternMatchesPath: true}
		shouldBeAdded, err := repoShouldBeAdded(context.Background(), zoekt, repo, pat)
		if err != nil {
			t.Fatal(err)
		}
		if shouldBeAdded {
			t.Errorf("Expected shouldBeAdded for repo %v to be false, but got true", repo)
		}
	})

	t.Run("repo should be included in results, query has -repoHasFile filter", func(t *testing.T) {
		repo := &search.RepositoryRevisions{Repo: &types.Repo{Name: "foo/no-match"}, Revs: []search.RevisionSpecifier{{RevSpec: ""}}}
		mockSearchFilesInRepos = func(args *search.TextParameters) (matches []*FileMatchResolver, common *searchResultsCommon, err error) {
			return []*FileMatchResolver{}, &searchResultsCommon{}, nil
		}
		pat := &search.TextPatternInfo{Pattern: "", FilePatternsReposMustExclude: []string{"foo"}, IsRegExp: true, FileMatchLimit: 1, PathPatternsAreCaseSensitive: false, PatternMatchesContent: true, PatternMatchesPath: true}
		shouldBeAdded, err := repoShouldBeAdded(context.Background(), zoekt, repo, pat)
		if err != nil {
			t.Fatal(err)
		}
		if !shouldBeAdded {
			t.Errorf("Expected shouldBeAdded for repo %v to be true, but got false", repo)
		}
	})
}

// repoShouldBeAdded determines whether a repository should be included in the result set based on whether the repository fits in the subset
// of repostiories specified in the query's `repohasfile` and `-repohasfile` fields if they exist.
func repoShouldBeAdded(ctx context.Context, zoekt *searchbackend.Zoekt, repo *search.RepositoryRevisions, pattern *search.TextPatternInfo) (bool, error) {
	repos := []*search.RepositoryRevisions{repo}
	args := search.TextParameters{
		PatternInfo: pattern,
		Zoekt:       zoekt,
	}
	rsta, err := reposToAdd(ctx, &args, repos)
	if err != nil {
		return false, err
	}
	return len(rsta) == 1, nil
}
