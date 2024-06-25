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
				Name:                    pulumi.String(team.Name),
				CreateDefaultMaintainer: pulumi.Bool(false),
				Description:             pulumi.String(team.Description),
				Privacy:                 pulumi.String(team.Privacy),
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
				HasDownloads:             pulumi.Bool(repo.HasDownloads),
				HasIssues:                pulumi.Bool(repo.HasIssues),
				HasProjects:              pulumi.Bool(repo.HasProjects),
				HasWiki:                  pulumi.Bool(repo.HasWiki),
				LicenseTemplate:          pulumi.String(repo.LicenseTemplate),
				Topics:                   pulumi.ToStringArray(repo.Topics),
				VulnerabilityAlerts:      pulumi.Bool(repo.VulnerabilityAlerts),
				Visibility:               pulumi.String(repo.Visibility),
				IsTemplate:               pulumi.Bool(repo.IsTemplate),
				WebCommitSignoffRequired: pulumi.Bool(repo.WebCommitSignoffRequired),
			}

			if repo.Pages.BuildType == "workflow" {
				repoPages := &github.RepositoryPagesArgs{
					BuildType: pulumi.String("workflow"),
				}
				if repo.Pages.CNAME != "" {
					repoPages.Cname = pulumi.String(repo.Pages.CNAME)
				}
				repoSync.Pages = repoPages
			} else if repo.Pages.Branch != "" {
				repoPages := &github.RepositoryPagesArgs{}

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

				repoSync.Pages = repoPages
			}

			if repo.Template.Owner != "" && repo.Template.Repository != "" {
				repoSync.Template = &github.RepositoryTemplateArgs{
					Owner:      pulumi.String(repo.Template.Owner),
					Repository: pulumi.String(repo.Template.Repository),
				}
			}

			newRepo, err := github.NewRepository(ctx, repo.Name, repoSync, pulumi.Protect(true))
			if err != nil {
				return err
			}

			_, err = github.NewBranchDefault(ctx, repo.Name, &github.BranchDefaultArgs{
				Branch:     pulumi.String(repo.DefaultBranch),
				Repository: pulumi.String(repo.Name),
			})
			if err != nil {
				return err
			}

			for _, protection := range repo.BranchesProtection {
				var pushRestrictionsID []string
				for _, pushRestTeamOrUser := range protection.PushRestrictions {
					team, err := github.LookupTeam(ctx, &github.LookupTeamArgs{
						Slug: strings.ToLower(strings.ReplaceAll(pushRestTeamOrUser, " ", "-")),
					}, nil)
					if err != nil {
						user, err := github.GetUser(ctx, &github.GetUserArgs{Username: pushRestTeamOrUser})
						if err != nil {
							return err
						}
						pushRestrictionsID = append(pushRestrictionsID, user.NodeId)
					} else {
						pushRestrictionsID = append(pushRestrictionsID, team.NodeId)
					}
				}

				var dismissalRestrictionsID []string
				for _, dismissRestrictionTeam := range protection.DismissalRestrictions {
					team, err := github.LookupTeam(ctx, &github.LookupTeamArgs{
						Slug: strings.ToLower(strings.ReplaceAll(dismissRestrictionTeam, " ", "-")),
					}, nil)
					if err != nil {
						return err
					}
					dismissalRestrictionsID = append(dismissalRestrictionsID, team.NodeId)
				}

				var pullRequestBypassersID []string
				for _, teamOrUser := range protection.PullRequestBypassers {
					team, err := github.LookupTeam(ctx, &github.LookupTeamArgs{
						Slug: strings.ToLower(strings.ReplaceAll(teamOrUser, " ", "-")),
					}, nil)
					if err != nil {
						user, err := github.GetUser(ctx, &github.GetUserArgs{Username: teamOrUser})
						if err != nil {
							return err
						}
						pullRequestBypassersID = append(pullRequestBypassersID, user.NodeId)
					} else {
						pullRequestBypassersID = append(pullRequestBypassersID, team.NodeId)
					}
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
