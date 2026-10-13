package main

import (
	"fmt"
	"log"
	"os"
	"path"
	"strings"

	github "github.com/pulumi/pulumi-github/sdk/v6/go/github"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"

	ghData "github.com/sigstore/github-sync/pkg/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		conf := config.New(ctx, "")
		ghConfig := conf.Require("github-data-directory")

		stat, err := os.Stat(ghConfig)
		if err != nil {
			log.Fatalf("Failed to stat %s: %v\n", ghConfig, err)
		}
		p := ghData.NewParser()

		if stat.IsDir() {
			err = p.ParseDir(ghConfig)
		} else {
			err = p.ParseFile(ghConfig, path.Dir(ghConfig))
		}
		if err != nil {
			log.Fatalf("Failed to load config: %v\n", err)
		}

		// sync custom roles
		for _, customRole := range p.Config.CustomRoles {
			roleArgs := &github.OrganizationCustomRoleArgs{
				BaseRole:    pulumi.String(customRole.BaseRole),
				Description: pulumi.String(customRole.Description),
				Permissions: pulumi.ToStringArray(customRole.Permissions),
			}
			_, err := github.NewOrganizationCustomRole(ctx, customRole.Name, roleArgs)
			if err != nil {
				return err
			}
		}

		// sync users
		for _, member := range p.Config.Users {
			_, err := github.NewMembership(ctx, member.Username, &github.MembershipArgs{
				Role:     pulumi.String(member.Role),
				Username: pulumi.String(member.Username),
			}, pulumi.Protect(true))
			if err != nil {
				return err
			}
		}

		for _, team := range p.Config.Teams {
			syncedTeams := strings.ToLower(strings.ReplaceAll(team.Name, " ", "-"))

			teamArgs := &github.TeamArgs{
				Name:        pulumi.String(team.Name),
				Description: pulumi.String(team.Description),
				Privacy:     pulumi.String(team.Privacy),
			}
			if team.ParentTeamID != 0 {
				teamArgs.ParentTeamId = pulumi.String(fmt.Sprintf("%d", team.ParentTeamID))
			}
			syncedTeam, err := github.NewTeam(ctx, syncedTeams, teamArgs, pulumi.Protect(true))
			if err != nil {
				return err
			}

			// sync users in teams
			for _, member := range p.Config.Users {
				for _, userTeam := range member.Teams {
					if userTeam.Name == team.Name {
						_, err = github.NewTeamMembership(ctx, fmt.Sprintf("%s-%s", member.Username, strings.ToLower(syncedTeams)), &github.TeamMembershipArgs{
							TeamId:   syncedTeam.ID(),
							Username: pulumi.String(member.Username),
							Role:     pulumi.String(userTeam.Role),
						})
						if err != nil {
							return err
						}
					}
				}
			}
		}

		for _, repo := range p.Config.Repositories {
			// sync repos
			repoSync := &github.RepositoryArgs{
				Name:                     pulumi.String(repo.Name),
				Description:              pulumi.String(repo.Description),
				HomepageUrl:              pulumi.String(repo.HomepageURL),
				AllowAutoMerge:           pulumi.Bool(repo.AllowAutoMerge),
				AllowMergeCommit:         pulumi.Bool(repo.AllowMergeCommit),
				AllowRebaseMerge:         pulumi.Bool(repo.AllowRebaseMerge),
				AllowSquashMerge:         pulumi.Bool(repo.AllowSquashMerge),
				AutoInit:                 pulumi.Bool(repo.AutoInit),
				Archived:                 pulumi.Bool(repo.Archived),
				DeleteBranchOnMerge:      pulumi.Bool(repo.DeleteBranchOnMerge),
				HasDiscussions:           pulumi.Bool(repo.HasDiscussions),
				HasIssues:                pulumi.Bool(repo.HasIssues),
				HasProjects:              pulumi.Bool(repo.HasProjects),
				HasWiki:                  pulumi.Bool(repo.HasWiki),
				LicenseTemplate:          pulumi.String(repo.LicenseTemplate),
				Topics:                   pulumi.ToStringArray(repo.Topics),
				Visibility:               pulumi.String(repo.Visibility),
				IsTemplate:               pulumi.Bool(repo.IsTemplate),
				WebCommitSignoffRequired: pulumi.Bool(repo.WebCommitSignoffRequired),
			}

			if repo.Template.Owner != "" && repo.Template.Repository != "" {
				repoSync.Template = &github.RepositoryTemplateArgs{
					Owner:      pulumi.String(repo.Template.Owner),
					Repository: pulumi.String(repo.Template.Repository),
				}
			}

			newRepo, err := github.NewRepository(ctx, repo.Name, repoSync, pulumi.Protect(true), pulumi.IgnoreChanges([]string{"pages"}))
			if err != nil {
				return err
			}

			if !repo.Archived {
				_, err = github.NewRepositoryVulnerabilityAlerts(ctx, repo.Name, &github.RepositoryVulnerabilityAlertsArgs{
					Enabled:    pulumi.Bool(repo.VulnerabilityAlerts),
					Repository: pulumi.String(repo.Name),
				})
				if err != nil {
					return err
				}
			}

			if repo.Pages.BuildType == "workflow" {
				repoPages := &github.RepositoryPagesArgs{
					Repository: pulumi.String(repo.Name),
					BuildType:  pulumi.String("workflow"),
				}
				if repo.Pages.CNAME != "" {
					repoPages.Cname = pulumi.String(repo.Pages.CNAME)
				}
				var source *github.RepositoryPagesSourceArgs
				if repo.Pages.Branch != "" {
					source = &github.RepositoryPagesSourceArgs{
						Branch: pulumi.String(repo.Pages.Branch),
					}
				}

				if repo.Pages.Path != "" {
					if source == nil {
						source = &github.RepositoryPagesSourceArgs{}
					}
					source.Path = pulumi.String(repo.Pages.Path)
				}

				repoPages.Source = source

				_, err = github.NewRepositoryPages(ctx, repo.Name, repoPages)
				if err != nil {
					return err
				}
			} else if repo.Pages.Branch != "" {
				repoPages := &github.RepositoryPagesArgs{
					Repository: pulumi.String(repo.Name),
				}

				source := &github.RepositoryPagesSourceArgs{
					Branch: pulumi.String(repo.Pages.Branch),
				}

				if repo.Pages.Path != "" {
					source.Path = pulumi.String(repo.Pages.Path)
				}

				repoPages.Source = source

				if repo.Pages.CNAME != "" {
					repoPages.Cname = pulumi.String(repo.Pages.CNAME)
				}

				_, err = github.NewRepositoryPages(ctx, repo.Name, repoPages)
				if err != nil {
					return err
				}
			}

			_, err = github.NewBranchDefault(ctx, repo.Name, &github.BranchDefaultArgs{
				Branch:     pulumi.String(repo.DefaultBranch),
				Repository: pulumi.String(repo.Name),
			})
			if err != nil {
				return err
			}

			for _, protection := range repo.BranchesProtection {
				pushRestrictionsID, err := lookupActorNodeIDs(ctx, protection.PushRestrictions)
				if err != nil {
					return err
				}

				dismissalRestrictionsID, err := lookupActorNodeIDs(ctx, protection.DismissalRestrictions)
				if err != nil {
					return err
				}

				pullRequestBypassersID, err := lookupActorNodeIDs(ctx, protection.PullRequestBypassers)
				if err != nil {
					return err
				}

				branchProtectionArgs := &github.BranchProtectionArgs{
					RepositoryId:                  newRepo.NodeId,
					Pattern:                       pulumi.String(protection.Pattern),
					EnforceAdmins:                 pulumi.Bool(protection.EnforceAdmins),
					AllowsDeletions:               pulumi.Bool(protection.AllowsDeletions),
					AllowsForcePushes:             pulumi.Bool(protection.AllowsForcePushes),
					RequiredLinearHistory:         pulumi.Bool(protection.RequiredLinearHistory),
					RequireSignedCommits:          pulumi.Bool(protection.RequireSignedCommits),
					RequireConversationResolution: pulumi.Bool(protection.RequireConversationResolution),
					RequiredStatusChecks: github.BranchProtectionRequiredStatusCheckArray{
						&github.BranchProtectionRequiredStatusCheckArgs{
							Strict:   pulumi.Bool(protection.RequireBranchesUpToDate),
							Contexts: pulumi.ToStringArray(protection.StatusChecks),
						},
					},
					RequiredPullRequestReviews: github.BranchProtectionRequiredPullRequestReviewArray{
						&github.BranchProtectionRequiredPullRequestReviewArgs{
							DismissStaleReviews:          pulumi.Bool(protection.DismissStaleReviews),
							RestrictDismissals:           pulumi.Bool(protection.RestrictDismissals),
							RequireCodeOwnerReviews:      pulumi.Bool(protection.RequireCodeOwnerReviews),
							RequiredApprovingReviewCount: pulumi.Int(protection.RequiredApprovingReviewCount),
							DismissalRestrictions:        pulumi.ToStringArray(dismissalRestrictionsID),
							PullRequestBypassers:         pulumi.ToStringArray(pullRequestBypassersID),
							RequireLastPushApproval:      pulumi.Bool(protection.RequireLastPushApproval),
						},
					},
				}

				if len(pushRestrictionsID) > 0 {
					// if project does not list any users in pushRestrictions, assume no push restriction
					branchProtectionArgs.RestrictPushes = github.BranchProtectionRestrictPushArray{
						&github.BranchProtectionRestrictPushArgs{
							PushAllowances: pulumi.ToStringArray(pushRestrictionsID),
						},
					}
				}

				_, err = github.NewBranchProtection(ctx, fmt.Sprintf("%s-%s", repo.Name, protection.Pattern), branchProtectionArgs)
				if err != nil {
					return err
				}
			}

			for _, collaborator := range repo.Collaborators {
				// sync collaborators
				_, err := github.NewRepositoryCollaborator(ctx, fmt.Sprintf("%s-%s", repo.Name, collaborator.Username), &github.RepositoryCollaboratorArgs{
					Permission:                pulumi.String(collaborator.Permission),
					Repository:                pulumi.String(repo.Name),
					Username:                  pulumi.String(collaborator.Username),
					PermissionDiffSuppression: pulumi.Bool(false),
				})
				if err != nil {
					return err
				}
			}

			for _, team := range repo.Teams {
				// sync teams for a repo
				// format the team name to be the team slug, eg. "My Team" become "my-team"
				formatedTeam := strings.ToLower(strings.ReplaceAll(team.Name, " ", "-"))
				teamID := formatedTeam
				// used when importing existing team
				if team.ID != "" {
					teamID = team.ID
				}
				_, err := github.NewTeamRepository(ctx, fmt.Sprintf("%s-%s", repo.Name, formatedTeam), &github.TeamRepositoryArgs{
					Permission: pulumi.String(team.Permission),
					Repository: pulumi.String(repo.Name),
					TeamId:     pulumi.String(teamID),
				})
				if err != nil {
					return err
				}
			}
		}

		return nil
	})
}

func lookupActorNodeIDs(ctx *pulumi.Context, actors []string) ([]string, error) {
	nodeIDs := make([]string, 0, len(actors))
	for _, actor := range actors {
		nodeID, err := lookupActorNodeID(ctx, actor)
		if err != nil {
			return nil, err
		}
		nodeIDs = append(nodeIDs, nodeID)
	}
	return nodeIDs, nil
}

func lookupActorNodeID(ctx *pulumi.Context, actor string) (string, error) {
	// GitHub Apps must be specified with a "[bot]" suffix (e.g. "my-app[bot]").
	// GitHub Users and GitHub Apps live in separate global namespaces that do not
	// reserve each other's names, so falling back between GetUser and GetGithubApp
	// for the same string would allow username/app-slug squatting. Normal GitHub
	// usernames and team slugs cannot contain brackets.
	if appSlug, ok := strings.CutSuffix(actor, "[bot]"); ok {
		app, err := github.GetGithubApp(ctx, &github.GetGithubAppArgs{Slug: appSlug})
		if err != nil {
			return "", err
		}
		return app.NodeId, nil
	}

	team, err := github.LookupTeam(ctx, &github.LookupTeamArgs{
		Slug: strings.ToLower(strings.ReplaceAll(actor, " ", "-")),
	}, nil)
	if err == nil {
		return team.NodeId, nil
	}

	user, err := github.GetUser(ctx, &github.GetUserArgs{Username: actor})
	if err == nil {
		return user.NodeId, nil
	}

	return "", err
}
