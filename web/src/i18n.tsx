import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Globe } from 'lucide-react'

export type Locale = 'ko' | 'en'
export const LANGUAGE_STORAGE_KEY = 'roborev-dashboard-language'

type MessageValues = Record<string, string | number>

// Keep the English catalog as the source of truth for the supported message
// keys.  The Korean catalog below is checked against this type so a newly
// added piece of Dashboard chrome cannot silently miss a translation.
export const en = {
  'language.label': 'Language',
  'language.select': 'Select language',
  'language.ko': '한국어',
  'language.en': 'English',
  'nav.openMenu': 'Open navigation menu',
  'nav.overview': 'Overview',
  'nav.reviews': 'Reviews',
  'nav.fixRefine': 'Fix & Refine',
  'nav.analyze': 'Analyze',
  'nav.tasks': 'Tasks',
  'nav.repositories': 'Repositories',
  'nav.agentsSetup': 'Agents & Setup',
  'nav.configuration': 'Configuration',
  'nav.stats': 'Stats & Insights',
  'stats.aria': 'Stats and insights',
  'stats.eyebrow': 'Repository analytics',
  'stats.title': 'Stats & Insights',
  'stats.description': 'Authoritative RoboRev statistics for the selected repository.',
  'stats.refresh': 'Refresh',
  'stats.window': 'Window',
  'stats.window.24h': '24 hours',
  'stats.window.7d': '7 days',
  'stats.window.30d': '30 days',
  'stats.window.90d': '90 days',
  'stats.branch': 'Branch scope',
  'stats.branch.all': 'All branches',
  'stats.branch.current': 'Current branch',
  'stats.overview': 'Overview',
  'stats.summary': 'Review summary',
  'stats.jobsTotal': 'Jobs',
  'stats.completed': 'Completed',
  'stats.passed': 'Passed',
  'stats.findings': 'Findings',
  'stats.passRate': 'Pass rate',
  'stats.resolutionRate': 'Resolution rate',
  'stats.addressed': 'Addressed',
  'stats.durationKind': 'Duration type',
  'stats.reviewDuration': 'Review',
  'stats.queueDuration': 'Queue',
  'stats.failed': 'Failed',
  'stats.errors': 'Errors',
  'stats.median': 'Median',
  'stats.failureTotal': 'Failures',
  'stats.retries': 'Retries',
  'stats.noVerdict': 'No verdict',
  'stats.windowCost': 'Approx. window cost (lower bound)',
  'stats.timing': 'Timing',
  'stats.duration': 'Duration',
  'stats.average': 'Average',
  'stats.p50': 'P50',
  'stats.p95': 'P95',
  'stats.agents': 'Agents',
  'stats.agent': 'Agent',
  'stats.jobs': 'Jobs',
  'stats.verdicts': 'Verdicts',
  'stats.jobTypes': 'Job types',
  'stats.failures': 'Failure categories',
  'stats.cost': 'Cost',
  'stats.costTitle': 'Approximate cost coverage',
  'stats.allTimeCost': 'Approx. all-time cost (lower bound)',
  'stats.jobsWithCost': 'Jobs with cost',
  'stats.costJobsTotal': 'Cost jobs total',
  'stats.costIncomplete': 'Some eligible jobs did not report cost; actual spend may be higher than this lower bound.',
  'stats.costCoverage': 'Reported free usage is shown as $0.00. Cost coverage is approximate.',
  'stats.costCoverageCount': 'Coverage: {withCost}/{total} jobs with reported cost.',
  'stats.windowCostNote': 'Window estimate is a lower bound; {withCost}/{total} jobs have reported cost.',
  'stats.costEmpty': 'No eligible cost-reporting jobs are in this scope yet.',
  'stats.insights': 'Insights',
  'stats.insightsDisabled': 'Guideline Insights are not enabled for this checkpoint.',
  'stats.guidelineTitle': 'Guideline Insights',
  'stats.guidelineDescription': 'Analyze recent failing reviews and current guidelines for recurring patterns and read-only recommendations.',
  'stats.guidelineQuota': 'LLM quota or provider cost may be used.',
  'stats.guidelineNoConfig': 'Recommendations only. Dashboard will not change configuration or .roborev.toml.',
  'stats.guidelineWarningUnknown': 'An additional Insights safety warning applies.',
  'stats.guidelinePrepare': 'Prepare Insights',
  'stats.guidelineRun': 'Run Insights',
  'stats.guidelineConfirmEyebrow': 'Explicit confirmation',
  'stats.guidelineConfirmTitle': 'Confirm Guideline Insights',
  'stats.guidelineExecutable': 'Executable',
  'stats.guidelineArguments': 'Arguments',
  'stats.guidelineState.queued': 'Queued',
  'stats.guidelineState.running': 'Running',
  'stats.guidelineState.done': 'Done',
  'stats.guidelineState.failed': 'Failed',
  'stats.guidelineState.canceled': 'Canceled',
  'stats.guidelineState.skipped': 'Skipped',
  'stats.guidelineState.unverified': 'Unverified',
  'stats.guidelineAmbiguous': 'Launch outcome could not be verified. A RoboRev Insights job may already have been queued. Do not retry automatically; prepare a new run if needed.',
  'stats.guidelineRepository': 'Repository',
  'stats.guidelineWindow': 'Insights window',
  'stats.guidelineBranch': 'Insights branch scope',
  'stats.guidelineAgent': 'Agent configuration',
  'stats.guidelineDefaultAgent': 'Use RoboRev configured default agent/model/reasoning',
  'stats.guidelineDisclosure': 'RoboRev may send recent failing review outputs, metadata, comments, addressed state, and resolved review guidelines for analysis.',
  'stats.guidelineLifetime': 'The daemon job continues independently if this page is closed. This page tracks only the current run.',
  'stats.guidelineSkipped': 'No failing reviews found in this time window. No LLM job was launched.',
  'stats.guidelineUnverified': 'Launch outcome could not be verified. A RoboRev Insights job may already have been queued. Do not retry automatically; prepare a new run if needed.',
  'stats.guidelineJobRunning': 'Insights job #{id} is {status}.',
  'stats.guidelineReceipt': 'Current Insights job: #{id}. The daemon continues independently of this page.',
  'stats.insightsMalformed': 'The Dashboard could not validate the Insights response.',
  'stats.guidelineResultFailed': 'The Insights result could not be loaded. Try again when the daemon is available.',
  'stats.guidelineRetryResult': 'Retry result',
  'stats.insightsCopyFailed': 'Clipboard access was unavailable.',
  'stats.loading': 'Loading…',
  'stats.unavailable': 'Stats unavailable',
  'stats.unavailableBody': 'Stats require a running daemon and a supported backend capability.',
  'stats.openSetup': 'Open Agents & Setup',
  'stats.noData': 'No data reported.',
  'stats.noFailures': 'No failure categories reported.',
  'stats.empty': 'No jobs were reported for this window.',
  'nav.advanced': 'Advanced',
  'nav.soon': 'Soon',
  'brand.dashboard': 'Dashboard',
  'status.daemonOnline': 'Daemon online',
  'status.daemonOffline': 'Daemon offline',
  'status.setupRequired': 'Setup required',
  'status.pass': 'Pass',
  'status.finding': 'Finding',
  'status.done': 'Done',
  'status.running': 'Running',
  'status.queued': 'Queued',
  'status.canceled': 'Canceled',
  'status.error': 'Error',
  'status.unknown': 'Unknown',
  'status.unknownAgent': 'Unknown agent',
  'status.open': 'Open',
  'status.closed': 'Closed',
  'status.dirty': 'Dirty',
  'status.clean': 'Clean',
  'status.ready': 'Ready',
  'status.stopped': 'Stopped',
  'status.processing': 'Processing',
  'status.paused': 'Paused',
  'status.offline': 'Offline',
  'common.cancel': 'Cancel',
  'common.close': 'Close',
  'common.copy': 'Copy',
  'common.copied': 'Copied',
  'common.retry': 'Retry',
  'common.refresh': 'Refresh',
  'common.loading': 'Loading…',
  'common.default': 'default',
  'common.defaultModel': 'default model',
  'common.none': 'None',
  'common.yes': 'Yes',
  'common.no': 'No',
  'common.chars': 'chars',
  'common.progress': '{phase} progress',
  'review.reviewHead': 'Review HEAD',
  'review.mode': 'Review mode',
  'review.selectPanel': 'Select a review panel',
  'review.panelAria': 'Review panel',
  'review.singleAgent': 'Single agent · no panel',
  'review.crossReview': 'Cross review · {panel}',
  'review.runningCount': '{count} running',
  'review.operations': 'Review operations',
  'review.queue': 'Review queue',
  'review.liveJobs': 'Live RoboRev jobs for the selected repository. Heavy content loads on demand.',
  'review.search': 'Search reviews',
  'review.searchPlaceholder': 'Search jobs, agents, refs…',
  'review.status': 'Status',
  'review.target': 'Target',
  'review.reviewer': 'Reviewer',
  'review.branch': 'Branch',
  'review.elapsed': 'Elapsed',
  'review.state': 'State',
  'review.customTask': 'Custom task',
  'review.panelSynthesis': 'Panel synthesis',
  'review.title': 'Review #{id}',
  'review.detail': 'Review detail',
  'review.detailAria': 'Review detail',
  'review.closeDetail': 'Close detail',
  'review.detailTabs': 'Review detail tabs',
  'review.tabReview': 'Review',
  'review.tabLog': 'Log',
  'review.tabPrompt': 'Prompt',
  'review.tabComments': 'Comments',
  'review.tabMetadata': 'Metadata',
  'review.panelMembers': 'Panel members',
  'review.memberProgress': '{terminal} / {total} terminal',
  'review.memberDetail': 'Panel member detail',
  'review.returnToSynthesis': 'Back to panel synthesis',
  'review.memberAgent': 'Agent',
  'review.memberModel': 'Model',
  'review.memberStatus': 'Status',
  'review.memberVerdict': 'Verdict',
  'review.memberDuration': 'Duration',
  'review.noOutput': 'No review output yet',
  'review.runningUpdates': 'Running reviews update automatically.',
  'review.noMemberJobs': 'No member jobs',
  'review.noMemberJobsBody': 'This is a single-agent review or member metadata is unavailable.',
  'review.detailRefreshFailed': 'Review detail could not be refreshed: {error}',
  'review.membersRefreshFailed': 'Panel members could not be refreshed: {error}',
  'review.noContent': 'No {tab} is available.',
  'review.tabLoadFailed': '{tab} could not be loaded: {error}',
  'review.noReviews': 'No reviews found',
  'review.tryDifferentFilter': 'Try a different filter.',
  'review.createHead': 'Create a new review of HEAD to start.',
  'review.comment': 'Comment',
  'review.commentPlaceholder': 'Paste a detailed resolution note…',
  'review.addComment': 'Add comment',
  'review.commentClose': 'Comment & Close',
  'review.commentSavedFirst': 'Comment is saved first. This review closes only after that succeeds.',
  'review.copyFixPrompt': 'Copy Fix Prompt',
  'review.openFixPrompt': 'Open Fix Prompt',
  'review.cancel': 'Cancel',
  'review.close': 'Close',
  'review.reopen': 'Reopen',
  'review.rerun': 'Rerun same target/config',
  'review.rerunConfirm': 'Rerun the same frozen target and resolved configuration?\n\nActual RoboRev transport:\nPOST /api/job/rerun\n{"job_id":{id}}\n\nThis does not review the current HEAD.',
  'review.fixPrompt': 'Fix Prompt',
  'review.finding': 'Finding · Review #{id}',
  'review.fixPromptStale': 'Repository state changed after this review; verify the prompt before use.',
  'review.fixPromptGenerated': 'Generated from the selected review finding.',
  'review.fixPromptAria': 'Fix prompt',
  'review.fix': 'Fix',
  'review.openFixRefine': 'Open Fix & Refine',
  'review.panelRefineSummary': 'Panel Refine status',
  'review.panelRefineActive': 'Panel Refine is active',
  'review.panelRefineWorking': 'The panel workflow is running. Open Fix & Refine for details.',
  'review.panelRefineNeedsAttention': 'The latest panel run needs attention. Open Fix & Refine to inspect it.',
  'review.closeFixPrompt': 'Close fix prompt',
  'review.fixPromptCopied': 'Fix prompt for review #{id} copied.',
  'review.clipboardUnavailable': 'Clipboard is unavailable. The editable fix prompt remains open.',
  'review.newHeadQueued': 'New HEAD review enqueued for {head}.',
  'review.stateUpdated': 'RoboRev state updated.',
  'review.commentClosed': 'Comment saved and review closed.',
  'review.agentCheckCompleted': 'Agent check completed for {agent}.',
  'review.partialSuccess': 'Partial success: {error}',
  'review.memberFallback': 'Member #{id}',
  'tasks.eyebrow': 'Repository workspace',
  'tasks.title': 'Tasks',
  'tasks.description': 'Existing RoboRev custom task jobs for the selected repository.',
  'tasks.customTask': 'Custom Task',
  'tasks.executionDisabledScope': 'Execution is fail-closed in this Dashboard release.',
  'tasks.executionDisabledBadge': 'Execution disabled',
  'tasks.executionDisabledPlaceholder': 'Custom Task execution is disabled.',
  'tasks.promptLabel': 'Task prompt',
  'tasks.executionDisabledReason': 'RoboRev v0.64 does not expose the daemon\'s effective allow_unsafe_agents / disable_codex_sandbox state or global config identity through any machine-readable contract, so the Dashboard cannot verify that a Custom Task would run with Codex read-only tools. A job-level non-agentic default does not protect against a more permissive daemon configuration.',
  'tasks.executionDisabledHint': 'Task enqueue is blocked in both this UI and the backend API (task-execution-disabled). Existing task history and results below stay available.',
  'tasks.history': 'Recent runs',
  'tasks.historyDescription': 'Custom Tasks found in the latest {limit} repository jobs.',
  'tasks.noHistory': 'No Custom Tasks found in the latest {limit} repository jobs.',
  'tasks.historyFailed': 'Task history could not be loaded: {error}',
  'tasks.viewResult': 'View result',
  'config.description': 'Inspect the RoboRev CLI effective configuration and change only explicitly allowlisted settings. The daemon is not required.',
  'config.refresh': 'Refresh configuration',
  'config.unavailable': 'The installed RoboRev version does not expose the bounded configuration contract.',
  'config.malformed': 'The Dashboard could not validate the RoboRev configuration response.',
  'config.loadFailed': 'Configuration could not be loaded: {error}',
  'config.effectiveTitle': 'Effective configuration',
  'config.effectiveHint': 'RoboRev CLI effective config is the source of truth. Some settings may affect future jobs or require daemon lifecycle behavior according to RoboRev.',
  'config.columnKey': 'Key',
  'config.columnValue': 'Value',
  'config.origin': 'Origin',
  'config.origin.global': 'Global',
  'config.origin.local': 'Repository',
  'config.origin.default': 'Default',
  'config.noRows': 'No effective rows were reported.',
  'config.editable': 'Editable settings',
  'config.currentValue': 'Current effective value',
  'config.unset': '(not set)',
  'config.severityDisabled': 'all severities (filtering disabled)',
  'config.scope': 'Scope',
  'config.scope.global': 'Global configuration',
  'config.scope.local': 'Repository',
  'config.newValue': 'New value',
  'config.invalidValue': 'Enter a value inside the allowed range.',
  'config.prepare': 'Prepare change',
  'config.confirmEyebrow': 'Explicit confirmation',
  'config.confirmTitle': 'Confirm configuration change',
  'config.settingLabel': 'Setting',
  'config.command': 'Command',
  'config.confirmAction': 'Apply setting',
  'config.warning.local_toml_modified': '.roborev.toml will be modified.',
  'config.warning.tracked_dirty': 'If this file is tracked, the repository will become dirty.',
  'config.warning.normalize': 'RoboRev may normalize TOML formatting and comments while saving.',
  'config.warning.global_normalize': 'Saving the global configuration may normalize TOML formatting and comments.',
  'config.changed': 'Configuration changed.',
  'config.dirtyNote': 'Repository configuration changed. Review and commit/revert .roborev.toml manually.',
  'config.unverified': 'The configuration result could not be fully verified. Refresh Configuration to check the current value.',
  'config.setting.max_workers': 'Workers',
  'config.setting.review_context_count': 'Review context count',
  'config.setting.auto_close_passing_reviews': 'Auto-close passing reviews',
  'config.setting.review_reasoning': 'Review reasoning',
  'config.setting.refine_reasoning': 'Refine reasoning',
  'config.setting.review_min_severity': 'Review minimum severity',
  'config.setting.fix_min_severity': 'Fix minimum severity',
  'config.setting.refine_min_severity': 'Refine minimum severity',
  'analyze.aria': 'Analyze repository',
  'analyze.title': 'Analyze',
  'analyze.description': 'Inspect RoboRev Analyze types and prompt templates from the installed version. Execution is disabled in Dashboard.',
  'analyze.type': 'Analysis type',
  'analyze.typeDescription': 'Types are discovered from the installed RoboRev version.',
  'analyze.loadingPrompt': 'Loading prompt…',
  'analyze.showPrompt': 'View prompt template',
  'analyze.promptFailed': 'Prompt template could not be loaded: {error}',
  'analyze.promptTitle': 'Prompt template',
  'analyze.copyPrompt': 'Copy prompt',
  'analyze.copyingPrompt': 'Copying prompt…',
  'analyze.promptCopied': 'Prompt template copied.',
  'analyze.promptCopyFailed': 'Prompt template could not be copied. Clipboard access was denied.',
  'analyze.safety': 'Analyze execution safety',
  'analyze.safetyTitle': 'Execution disabled',
  'analyze.agenticWarning': 'RoboRev v0.64 Analyze is an agentic job. The configured agent may modify files and execute commands.',
  'analyze.executionDisabled': 'Dashboard cannot safely own the working-tree lifecycle, so execution is disabled. Catalog and prompt inspection remain available.',
  'analyze.executionDisabledTitle': 'Inspection only',
  'analyze.executionDisabledCta': 'Execution disabled',
  'analyze.executionDisabledReason': 'Use the catalog and prompt template to inspect the configured Analyze workflow.',
  'repo.repository': 'Repository',
  'repo.currentAria': 'Current repository',
  'repo.registeredTargets': 'Registered targets',
  'repo.onlyCanonical': 'Only canonical Git roots registered here can be mutation targets.',
  'repo.addLocal': 'Add local Git repository',
  'repo.pathPlaceholder': 'E:\\Dev\\GitHub\\project',
  'repo.register': 'Register',
  'repo.trackedEyebrow': 'RoboRev inventory',
  'repo.trackedTitle': 'RoboRev Tracked Repositories',
  'repo.trackedDescription': 'Read-only repository inventory reported by the RoboRev daemon.',
  'repo.trackedName': 'Name',
  'repo.trackedPath': 'Path',
  'repo.trackedJobs': 'Jobs',
  'repo.trackedStatus': 'Dashboard status',
  'repo.trackedSummary': 'Tracked repositories {count}, Jobs {jobs}',
  'repo.trackedRegistered': 'Dashboard registered',
  'repo.trackedNotRegistered': 'Not registered',
  'repo.trackedRefresh': 'Refresh',
  'repo.trackedLoading': 'Loading tracked repositories…',
  'repo.trackedEmpty': 'No repositories are tracked by RoboRev.',
  'repo.trackedUnavailable': 'RoboRev inventory unavailable',
  'repo.trackedUnavailableBody': 'A supported, healthy RoboRev daemon is required to read tracked repositories.',
  'repo.trackedError': 'Tracked repository inventory could not be loaded.',
  'repo.trackedMalformed': 'The Dashboard could not validate the RoboRev repository inventory.',
  'repo.rename': 'Rename',
  'repo.renameTitle': 'Rename RoboRev repository',
  'repo.renameDescription': 'Change the RoboRev display name only. The filesystem path and Dashboard registration stay unchanged.',
  'repo.renameCurrentName': 'Current name',
  'repo.renameNewName': 'New display name',
  'repo.renamePath': 'Tracked root path',
  'repo.renameJobs': 'Jobs',
  'repo.renameDashboardStatus': 'Dashboard status',
  'repo.renameCommand': 'Exact executable and argv',
  'repo.renameWarning': 'Display-name only: no filesystem move, Git operation, or Dashboard registration change.',
  'repo.renamePrepare': 'Prepare rename',
  'repo.renamePreparing': 'Preparing…',
  'repo.renameUnavailable': 'RoboRev is currently unavailable. When it becomes available again, prepare a new Rename Preview.',
  'repo.renameUnsupported': 'The installed RoboRev does not support repository rename.',
  'repo.renameConfirm': 'Confirm rename',
  'repo.renameConfirming': 'Renaming…',
  'repo.renameSuccess': 'Repository display name changed.',
  'repo.renameUnverified': 'The rename process started, but the fresh inventory could not verify the final name. Refresh to inspect the current state. No retry was issued.',
  'repo.renameFailed': 'Repository rename could not be completed: {error}',
  'repo.renameMalformed': 'The Dashboard could not validate the repository rename response.',
  'repo.renameInvalidEmpty': 'Enter a non-empty display name.',
  'repo.renameInvalidTooLong': 'Display names must be at most 128 Unicode characters.',
  'repo.renameInvalidUtf8': 'Enter valid Unicode text.',
  'repo.renameInvalidControl': 'Control characters are not allowed.',
  'repo.renameInvalidLeadingDash': 'Names beginning with “-” are not allowed.',
  'repo.renameInvalidSame': 'Enter a different display name.',
  'repo.renameClose': 'Close rename dialog',
  'repo.move': 'Move',
  'repo.moveTitle': 'Repair RoboRev repository path',
  'repo.moveDescription': 'Reconnect a stale RoboRev path to a repository already registered in this Dashboard.',
  'repo.moveSource': 'Stale tracked repository',
  'repo.moveSourcePath': 'Stale tracked path',
  'repo.moveJobs': 'Jobs',
  'repo.moveStatus': 'Dashboard status',
  'repo.moveDestination': 'Registered destination',
  'repo.moveDestinationPlaceholder': 'Select a registered repository',
  'repo.moveDestinationPath': 'Destination path',
  'repo.moveDestinationName': 'Destination name',
  'repo.moveWorkingDirectory': 'Working directory',
  'repo.moveCommand': 'Exact executable, working directory, and argv',
  'repo.moveWarning': 'This does not move a folder. It reconnects an already-moved repository in RoboRev records.',
  'repo.moveRegistrationWarning': 'The new path must already be registered in the Dashboard.',
  'repo.movePrepare': 'Prepare move',
  'repo.movePreparing': 'Preparing…',
  'repo.moveUnavailable': 'RoboRev is currently unavailable. When it becomes available again, prepare a new Move Preview.',
  'repo.moveUnsupported': 'The installed RoboRev does not support repository path repair.',
  'repo.moveConfirm': 'Confirm move',
  'repo.moveConfirming': 'Moving…',
  'repo.moveSuccess': 'RoboRev repository path repaired.',
  'repo.moveUnverified': 'The move process started, but the fresh inventory could not verify the final path. Refresh to inspect the current state. No retry was issued.',
  'repo.moveMalformed': 'The Dashboard could not validate the repository move response.',
  'repo.moveClose': 'Close repository move dialog',
  'repo.moveEligibility': 'Check path repair',
  'repo.moveEligibilityChecking': 'Checking path repair…',
  'repo.moveEligibilitySourceExists': 'This tracked path still exists on disk, so path repair is not needed.',
  'repo.moveEligibilityRegistered': 'This repository is already registered in the Dashboard.',
  'repo.moveEligibilityFailed': 'Path repair eligibility could not be checked.',
  'repo.moveEligibilityMalformed': 'The Dashboard could not validate the path repair eligibility response.',
  'repo.merge': 'Merge',
  'repo.mergeTitle': 'Merge stale RoboRev repository entry',
  'repo.mergeDescription': 'Consolidate a stale RoboRev source entry into a repository already registered in this Dashboard.',
  'repo.mergeSource': 'Stale source',
  'repo.mergeSourcePath': 'Source path',
  'repo.mergeSourceJobs': 'Source jobs',
  'repo.mergeTarget': 'Registered target',
  'repo.mergeTargetPlaceholder': 'Select a registered target',
  'repo.mergeTargetName': 'Target name',
  'repo.mergeTargetPath': 'Target path',
  'repo.mergeTargetJobs': 'Target jobs',
  'repo.mergeCommand': 'Exact executable, working directory, and argv',
  'repo.mergeWorkingDirectory': 'Working directory',
  'repo.mergeWarning': 'The stale RoboRev source entry will be removed and its jobs, reviews, and related metadata consolidated into the target.',
  'repo.mergeWarningNoRollback': 'The target name/path stay unchanged. No filesystem, Git, Dashboard Registry, automatic rollback, or retry is performed.',
  'repo.mergePrepare': 'Prepare merge',
  'repo.mergePreparing': 'Preparing…',
  'repo.mergeUnavailable': 'RoboRev is currently unavailable. When it becomes available again, prepare a new Merge Preview.',
  'repo.mergeUnsupported': 'The installed RoboRev does not support repository merge.',
  'repo.mergeConfirm': 'Confirm merge',
  'repo.mergeConfirming': 'Merging…',
  'repo.mergeSuccess': 'Stale RoboRev repository entry merged.',
  'repo.mergeUnverified': 'The merge process started, but fresh RoboRev inventory could not verify the consolidated state. Refresh to inspect the current state. No retry was issued.',
  'repo.mergeMalformed': 'The Dashboard could not validate the repository merge response.',
  'repo.mergeClose': 'Close repository merge dialog',
  'repo.mergeEligibility': 'Check merge',
  'repo.mergeEligibilityChecking': 'Checking merge eligibility…',
  'repo.mergeEligibilitySourceExists': 'This source path still exists on disk, so it is not a stale entry.',
  'repo.mergeEligibilityRegistered': 'This repository is already registered in the Dashboard.',
  'repo.mergeEligibilityEmpty': 'This stale entry has no jobs to merge.',
  'repo.mergeEligibilityActive': 'This stale entry has queued or running jobs, so it cannot be merged safely yet.',
  'repo.mergeEligibilityNoTargets': 'No registered RoboRev-tracked target is available.',
  'repo.mergeEligibilityFailed': 'Merge eligibility could not be checked.',
  'repo.mergeEligibilityMalformed': 'The Dashboard could not validate the merge eligibility response.',
  'panel.unavailable': 'Panel unavailable',
  'panel.unavailableNotInstalled': 'RoboRev is not installed.',
  'panel.unavailableDaemonOffline': 'The RoboRev daemon is not running.',
  'panel.loading': 'Loading review panels…',
  'panel.configurationUnavailable': 'Panel configuration unavailable',
  'panel.retryDiscovery': 'Retry panel discovery',
  'setup.runtimeHealth': 'Runtime health',
  'setup.runtime': 'RoboRev Runtime',
  'setup.daemon': 'Daemon',
  'setup.model': 'Model',
  'setup.runInit': 'Run',
  'setup.heading': 'Agents & Setup',
  'setup.authNotice': 'Dashboard uses existing agent CLI authentication and never stores tokens.',
  'setup.notInstalled': 'RoboRev is not installed',
  'setup.install': 'Install RoboRev',
  'setup.installStart': 'Install & Start',
  'setup.installConfirm': 'Install & Start RoboRev?',
  'setup.daemonStoppedToast': 'RoboRev daemon stopped.',
  'setup.upgradeRequired': 'RoboRev upgrade required',
  'setup.metadataContractUnavailable': 'This version does not expose the safe metadata and enqueue contracts required by the Dashboard.',
  'setup.startDaemonHint': 'Start the daemon to enqueue and view reviews.',
  'setup.configureBinaryHint': 'Configure a RoboRev binary to continue.',
  'setup.actionFailed': 'The setup action failed.',
  'setup.healthNotChecked': 'Not checked',
  'setup.healthPassed': 'Passed',
  'setup.healthFailed': 'Failed',
  'setup.healthCanceled': 'Canceled',
  'setup.healthSkipped': 'Skipped',
  'setup.installOnlyStep': 'This is the only step that downloads and installs software.',
  'setup.explicitConfirmation': 'Explicit confirmation',
  'setup.closeInstall': 'Close install confirmation',
  'setup.closeDaemon': 'Close daemon confirmation',
  'setup.closeAgentCheck': 'Close agent check confirmation',
  'setup.executionPreview': 'Execution preview',
  'setup.backendCommand': 'Backend-bound executable and exact typed arguments.',
  'setup.backendCommandSupplied': 'Command supplied by RoboRev',
  'setup.daemonStartedToast': 'RoboRev daemon started.',
  'setup.daemonRestartedToast': 'RoboRev daemon restarted.',
  'setup.versionNotDetected': 'not detected',
  'setup.pathUnavailable': 'PATH lookup unavailable',
  'setup.archiveSizeUnavailable': 'Backend-supplied exact archive size',
  'setup.daemonRunningDetail': 'The RoboRev daemon is running.',
  'setup.daemonNeedsAttention': 'RoboRev daemon needs attention',
  'setup.daemonNeedsAttentionDetail': 'The daemon is reachable, but its health or version validation failed.',
  'setup.daemonHealthFailure': 'The installed daemon is reachable but did not pass health/version validation ({code}).',
  'setup.daemonStoppedDetail': 'The verified installation is compatible with this Dashboard, but the daemon is stopped.',
  'setup.daemonNotInstalledDetail': 'RoboRev is not installed.',
  'setup.repository': 'Repository',
  'setup.releaseSource': 'Release source',
  'setup.version': 'Version',
  'setup.assetBytes': 'Asset bytes',
  'setup.target': 'Target',
  'setup.sha256': 'SHA-256',
  'setup.compatibility': 'Compatibility',
  'setup.officialRelease': 'Official GitHub release',
  'setup.verifiedTarget': 'Verified Dashboard target',
  'setup.checksumBackend': 'Checksum supplied by backend',
  'setup.compatibleWindows': 'Compatible with this Windows architecture.',
  'setup.verifyCompatibility': 'Backend will verify compatibility before installation.',
  'setup.willDo': 'Will do',
  'setup.willNotDo': 'Will not do',
  'setup.installScope': 'Explicit install scope',
  'setup.downloadExact': 'Download the exact {version} release.',
  'setup.verifyChecksum': 'Verify the full SHA-256 checksum.',
  'setup.installProbe': 'Install to the verified target and probe the binary.',
  'setup.startDaemonVerify': 'Start and verify the RoboRev daemon.',
  'setup.noSecurityChanges': 'Change Windows security policy or PATH.',
  'setup.noInit': 'Run roborev init.',
  'setup.noAgents': 'Install agents, hooks, or extensions.',
  'setup.noKeys': 'Read or store agent keys and tokens.',
  'setup.installWillDo': 'Download the exact {version} release, verify the full SHA-256 checksum, install and probe the verified binary, then start and verify the RoboRev daemon.',
  'setup.installWillNotDo': 'Change Windows security policy or PATH, run roborev init, install agents, hooks, or extensions, or read or store agent keys and tokens.',
  'setup.setupUnavailable': 'Setup state unavailable',
  'setup.setupResponseInvalid': 'The Dashboard could not validate the typed RoboRev setup response.',
  'setup.verifiedInstallation': 'Verified installation',
  'setup.requestedVersion': 'Requested version',
  'setup.architecture': 'Architecture',
  'setup.source': 'Source',
  'setup.openRelease': 'Open official release source',
  'setup.officialRepository': 'Official repository',
  'setup.asset': 'Asset',
  'setup.pendingInstallation': 'Pending installation',
  'setup.compatibleDashboard': 'Compatible with this Dashboard',
  'setup.notCompatible': 'Not compatible',
  'setup.pendingVerification': 'Pending verification',
  'setup.operationSerialized': 'Setup operations are serialized. Review and mutation controls remain disabled until the backend reports ready.',
  'setup.failedAction': 'The setup action failed.',
  'setup.daemonStopped': 'RoboRev daemon is stopped',
  'setup.daemonInstalledStopped': 'The verified RoboRev binary is installed. Start its daemon to enqueue reviews.',
  'setup.daemonStart': 'Start daemon',
  'setup.daemonRunning': 'Daemon Running',
  'setup.daemonOffline': 'Daemon Offline',
  'setup.daemonReady': 'Ready to accept typed review operations.',
  'setup.daemonNotAccepting': 'The daemon is not accepting new operations.',
  'setup.daemonControls': 'Daemon and queue controls',
  'setup.runtimeDescription': 'Typed controls reflect the daemon source of truth. Actions are serialized while an operation is active.',
  'setup.runtimeUnavailable': 'Runtime controls unavailable',
  'setup.runtimeRefresh': 'Refresh to retrieve the typed daemon and queue state.',
  'diagnostics.title': 'Runtime Diagnostics',
  'diagnostics.description': 'Read-only daemon, worker, queue, job, and health signals.',
  'diagnostics.open': 'Diagnostics',
  'diagnostics.collapse': 'Hide diagnostics',
  'diagnostics.refresh': 'Refresh diagnostics',
  'diagnostics.loading': 'Loading runtime diagnostics…',
  'diagnostics.unavailable': 'Unavailable / Offline',
  'diagnostics.unavailableBody': 'Runtime diagnostics are not available right now.',
  'diagnostics.retry': 'Retry diagnostics',
  'diagnostics.daemon': 'Daemon',
  'diagnostics.workers': 'Workers',
  'diagnostics.queue': 'Queue',
  'diagnostics.jobs': 'Jobs',
  'diagnostics.health': 'Health',
  'diagnostics.running': 'Running',
  'diagnostics.offline': 'Offline',
  'diagnostics.version': 'Version',
  'diagnostics.uptime': 'Uptime',
  'diagnostics.activeMax': 'Active / max',
  'diagnostics.paused': 'Paused',
  'diagnostics.processing': 'Processing',
  'diagnostics.queued': 'Queued',
  'diagnostics.runningJobs': 'Running jobs',
  'diagnostics.completed': 'Completed',
  'diagnostics.failed': 'Failed',
  'diagnostics.skipped': 'Skipped',
  'diagnostics.healthy': 'Healthy',
  'diagnostics.degraded': 'Degraded',
  'diagnostics.errors24h': 'Errors (24h)',
  'diagnostics.components': 'Components',
  'diagnostics.componentHealthy': 'Healthy',
  'diagnostics.componentUnhealthy': 'Unhealthy',
  'setup.reviewQueue': 'Review queue',
  'setup.queuePaused': 'Queue Paused',
  'setup.queueProcessing': 'Queue Processing',
  'setup.queueUnknown': 'Queue Unknown',
  'setup.queuePausedBody': 'Running jobs continue; new queued jobs will not start.',
  'setup.queueProcessingBody': 'Queued jobs can start when capacity is available.',
  'setup.queueUnknownBody': 'Queue state is unavailable while the daemon is offline or unknown.',
  'setup.pauseQueue': 'Pause queue',
  'setup.resumeQueue': 'Resume queue',
  'setup.daemonOfflineReason': 'Daemon is offline.',
  'setup.queueUnknownReason': 'Queue state is unknown. Refresh to retry.',
  'setup.controlsDisabled': 'Controls disabled: {reason}',
  'setup.panelActive': 'Panel Refine is active.',
  'setup.operationActive': 'A daemon operation is in progress.',
  'setup.configuredAgents': 'Configured Review Agents',
  'setup.agentDescription': 'Detection confirms executable/config presence. Health is separate and changes only after an explicit check.',
  'setup.agentsLoading': 'Loading configured agents…',
  'setup.agentsUnavailable': 'Configured agents unavailable',
  'setup.agentsRefresh': 'Refresh to retrieve the typed agent configuration.',
  'setup.noConfiguredAgents': 'No configured review agents were reported by RoboRev.',
  'setup.detected': 'Detected',
  'setup.health': 'Health',
  'setup.reasoning': 'Reasoning',
  'setup.command': 'Command',
  'setup.sourceLabel': 'Source: {source}',
  'setup.lastChecked': 'Last checked {at}',
  'setup.runCheck': 'Run check',
  'setup.agentMustDetected': 'Agent must be detected before a check can run.',
  'setup.defaultModel': 'Default model',
  'setup.executableDetected': 'Executable/configuration detected.',
  'setup.executableNotDetected': 'Executable or configuration was not detected.',
  'setup.confirmAgentCheck': 'Run agent check?',
  'setup.agentCheckDescription': 'This sends a small real request through RoboRev and may use subscription/API quota.',
  'setup.configuredAgent': 'Configured agent',
  'setup.canonicalAgent': 'Canonical agent',
  'setup.sendCheck': 'Send the check only through this selected, configured agent.',
  'setup.noConfigChange': 'Change configuration or store credentials in the Dashboard.',
  'setup.restartDaemon': 'Restart RoboRev daemon?',
  'setup.stopDaemon': 'Stop RoboRev daemon?',
  'setup.restartDaemonAction': 'Restart daemon',
  'setup.stopDaemonAction': 'Stop daemon',
  'setup.restartDescription': 'This stops and starts the daemon. Active jobs may be interrupted; queued jobs remain queued.',
  'setup.stopDescription': 'This stops the daemon. Active jobs may be interrupted; queued jobs remain queued.',
  'setup.requestRestart': 'Request one typed daemon restart through RoboRev.',
  'setup.requestStop': 'Request one typed daemon stop through RoboRev.',
  'setup.noDashboardStop': 'Delete reviews, change repository files, or stop jobs directly from the Dashboard.',
  'setup.phase.idle': 'Idle',
  'setup.phase.checking': 'Checking',
  'setup.phase.downloading': 'Downloading',
  'setup.phase.extracting': 'Extracting',
  'setup.phase.verifying': 'Verifying',
  'setup.phase.installing': 'Installing',
  'setup.phase.binding': 'Binding',
  'setup.phase.starting_daemon': 'Starting daemon',
  'setup.phase.waiting_for_daemon': 'Waiting for daemon',
  'setup.phase.ready': 'Ready',
  'setup.phase.failed': 'Failed',
  'setup.phase.blocked': 'Blocked',
  'setup.phase.canceled': 'Canceled',
  'setup.phase.unsupported': 'Unsupported',
  'setup.setupTitle': 'RoboRev setup · {phase}',
  'setup.setupWaiting': 'The Dashboard is waiting for the verified RoboRev installation to finish.',
  'setup.blockedTitle': 'RoboRev was blocked by Windows Application Control',
  'setup.blockedBody': 'The verified file was not started. Check Windows Security or your organization’s application-control policy; the Dashboard cannot disable or bypass it.',
  'setup.retryVerification': 'Retry verification',
  'setup.unsupportedTitle': 'RoboRev {version} is not supported',
  'setup.unsupportedBody': 'The Dashboard will not downgrade it automatically. Recommended version: {version}.',
  'setup.installRejected': 'RoboRev installation was rejected',
  'setup.setupFailed': 'RoboRev setup failed',
  'setup.failureCode': 'Failure code: {code}.',
  'setup.operationIncomplete': 'The typed setup operation did not complete.',
  'setup.retryInstallation': 'Retry installation',
  'setup.retryDaemonHealth': 'Retry daemon health',
  'setup.retryStart': 'Retry Start',
  'setup.setupCanceled': 'RoboRev setup was canceled',
  'setup.canceledBody': 'No partial installation is treated as ready. Start a new verified setup operation when appropriate.',
  'setup.retrySetup': 'Retry setup',
  'setup.installRelease': 'Install the verified {version} release to enable reviews and Panel Refine.',
  'setup.downloaded': '{bytes} downloaded',
  'setup.bytesProgress': '{percent}% · {downloaded} / {total}',
  'setup.progress': '{phase} progress',
  'panel.automatedGate': 'Automated quality gate',
  'panel.aria': 'Panel Refine',
  'panel.run': 'Run #{id}',
  'panel.reviewAt': 'Review {head}',
  'panel.fixAt': 'Fix #{attempt} · {agent}',
  'panel.pending': 'Pending',
  'panel.notConfigured': 'Not configured',
  'panel.notRun': 'Not run',
  'panel.failed': 'Failed',
  'panel.resultError': 'Error',
  'panel.resultCancelled': 'Cancelled',
  'panel.refreshedError': 'Panel Refine could not be refreshed: {error}',
  'panel.finalReview': 'Final review',
  'panel.needsAttention': 'Needs attention',
  'panel.titlePassed': 'Panel Refine passed',
  'panel.titleNeedsAttention': 'Panel Refine needs attention',
  'panel.titleFailed': 'Panel Refine failed',
  'panel.titleCanceled': 'Panel Refine cancelled',
  'panel.phaseStarting': 'Starting',
  'panel.phaseReviewing': 'Reviewing',
  'panel.phaseEvaluating': 'Evaluating',
  'panel.phaseFixing': 'Fixing',
  'panel.refineUnavailable': 'Panel Refine unavailable: {reason}',
  'panel.versionNoPanel': 'This RoboRev version does not support panel review.',
  'panel.versionNoFix': 'This RoboRev version does not support the typed fix command.',
  'panel.versionNoStatus': 'This RoboRev version does not support the bounded panel status contract.',
  'panel.description': 'Repeat the selected panel review, apply a Codex fix, and verify each new HEAD.',
  'panel.openReview': 'Open Review',
  'panel.totalReviews': 'Total reviews',
  'panel.fixAttempts': 'Fix attempts',
  'panel.iterationPanel': 'Iteration panel',
  'panel.finalPanel': 'Final panel',
  'panel.finalResult': 'Final panel result',
  'panel.fixAgent': 'Fix agent',
  'panel.startedHead': 'Started HEAD',
  'panel.currentHead': 'Current HEAD',
  'panel.elapsed': 'Elapsed',
  'panel.currentReview': 'Current review',
  'panel.none': 'None',
  'panel.cancelState': 'Repository state after cancellation',
  'panel.branch': 'Branch',
  'panel.branchStateUnavailable': 'HEAD / branch state unavailable',
  'panel.dirtyTree': 'Dirty working tree',
  'panel.cleanTree': 'Clean working tree',
  'panel.warningFinalState': 'Warning: the final repository state could not be verified. Inspect the working tree before continuing.',
  'panel.warningPartialChanges': 'Warning: cancellation may have left partial repository changes. Inspect the working tree before continuing.',
  'panel.timeline': 'Timeline',
  'panel.openFinalReview': 'Open Final Review',
  'panel.openLatestReview': 'Open Latest Review',
  'panel.cancel': 'Cancel Panel Refine',
  'panel.close': 'Close Panel Refine',
  'panel.start': 'Panel Refine',
  'panel.startAction': 'Start Panel Refine',
  'panel.dialogDescription': 'Panel Refine repeatedly reviews the current HEAD with the selected panel, uses the failed synthesis review to create a fix, commits the fix, and reviews the new HEAD until it passes or reaches the limit.',
  'panel.maxAttempts': 'Max fix attempts',
  'panel.default': ' (default)',
  'panel.finalDeep': 'Final deep panel',
  'panel.preview': 'Execution preview',
  'panel.optionsLoading': 'Loading backend command options…',
  'panel.previewDescription': 'Server-side fix adapter uses the backend-provided executable and exact typed argv. No shell command or executable is accepted from the browser.',
  'panel.started': 'Panel Refine started.',
  'panel.cancelRequested': 'Panel Refine cancellation requested.',
  'fix.eyebrow': 'Safe change workflow',
  'fix.aria': 'Fix and Refine',
  'fix.title': 'Fix & Refine',
  'fix.description': 'Preview a typed RoboRev fix for an open finding, then confirm it explicitly.',
  'fix.target': 'Fix target',
  'fix.targetDescription': 'Only terminal, open findings from a single review or panel synthesis can be fixed.',
  'fix.selectTarget': 'Finding to fix',
  'fix.chooseTarget': 'Choose an open finding',
  'fix.single': 'single review',
  'fix.synthesis': 'panel synthesis',
  'fix.noTargets': 'No eligible findings',
  'fix.noTargetsBody': 'Run a review of HEAD or open a finding from Reviews to prepare a safe fix.',
  'fix.job': 'Job',
  'fix.gitRef': 'Git ref',
  'fix.branch': 'Branch',
  'fix.targetKind': 'Target type',
  'fix.reviewPreview': 'Review execution preview',
  'fix.loadingPreview': 'Loading preview…',
  'fix.previewReady': 'Backend preview ready. No command has run.',
  'fix.previewFailed': 'Could not prepare this fix: {error}',
  'fix.executionPreview': 'Execution preview',
  'fix.confirmTitle': 'Confirm RoboRev fix',
  'fix.confirmDescription': 'The backend will revalidate this target before starting the process.',
  'fix.repository': 'Repository',
  'fix.command': 'Exact executable and argv',
  'fix.safety': 'This action may edit files and create a commit. Keep the repository clean and verify the result after completion.',
  'fix.startingHead': 'Starting HEAD',
  'fix.endingHead': 'Ending HEAD',
  'fix.endingBranch': 'Ending branch',
  'fix.endingDirty': 'Ending tree',
  'fix.operation': 'Fix operation #{id}',
  'fix.starting': 'Starting',
  'fix.confirmAction': 'Confirm and run fix',
  'fix.confirming': 'Starting…',
  'fix.started': 'Fix operation started.',
  'fix.cancel': 'Cancel fix',
  'fix.canceling': 'Canceling…',
  'fix.cancelRequested': 'Fix cancellation requested.',
  'fix.openReview': 'Open resulting review',
  'fix.closePreview': 'Close fix preview',
  'error.fixPreviewMalformed': 'The fix preview response was malformed.',
  'error.fixOperationMalformed': 'The fix operation response was malformed.',
  'error.fixPromptMalformed': 'The fix prompt response was malformed.',
  'error.setupMalformed': 'The RoboRev setup response was malformed.',
  'error.agentsMalformed': 'The configured-agent response was malformed.',
  'error.runtimeMalformed': 'The runtime-control response was malformed.',
  'error.panelConfigurationMalformed': 'The panel configuration response was malformed.',
  'error.panelRefineMalformed': 'The Panel Refine response was malformed.',
  'error.panelRefineNoValidRuns': 'The Panel Refine response contained no valid runs.',
  'error.panelRefineWrongRepository': 'The Panel Refine response belonged to a different repository.',
  'error.daemonOperationTimeout': 'The RoboRev daemon operation did not reach a terminal state before the timeout.',
  'error.reviewTargetMissing': 'Review was enqueued, but its pinned target metadata was missing.',
  'error.bootstrapRepositoryMissing': 'The selected repository metadata was missing from bootstrap.',
  'error.repositoryRestoreFailed': 'Repository state could not be restored: {error}',
  'error.analyzeCatalogMalformed': 'The Dashboard could not validate the RoboRev analysis catalog.',
  'error.analyzePromptMalformed': 'The Dashboard could not validate this RoboRev prompt template.',
  'error.dashboardFailed': 'Dashboard UI failed',
  'error.reload': 'Reload',
  'error.backgroundRefresh': 'Background refresh issue',
  'roadmap.title': 'Checkpoint roadmap',
  'roadmap.body': 'This typed workflow is tracked in ROBoreV-COVERAGE.md and will be added without a generic shell endpoint.',
} as const

export type MessageKey = keyof typeof en

// The catalog is intentionally kept compact in one literal.  Starting with
// the English source also makes an omitted translation safe while the typed
// override list below remains easy to audit.
export const ko: Record<MessageKey, string> = { ...en,
  'review.fix': '수정', 'review.openFixRefine': '수정 및 반복 검증 열기', 'review.panelRefineSummary': 'Panel Refine 상태', 'review.panelRefineActive': 'Panel Refine 실행 중', 'review.panelRefineWorking': '패널 workflow가 실행 중입니다. 자세한 내용은 수정 및 반복 검증에서 확인하세요.', 'review.panelRefineNeedsAttention': '최근 패널 실행에 확인이 필요합니다. 수정 및 반복 검증에서 확인하세요.',
  'fix.eyebrow': '안전한 변경 workflow', 'fix.aria': '수정 및 반복 검증', 'fix.title': '수정 및 반복 검증', 'fix.description': '열린 문제에 대한 typed RoboRev 수정을 미리 확인한 뒤 명시적으로 실행합니다.', 'fix.target': '수정 대상', 'fix.targetDescription': '단일 리뷰 또는 패널 종합의 종료된 열린 문제만 수정할 수 있습니다.', 'fix.selectTarget': '수정할 문제', 'fix.chooseTarget': '열린 문제 선택', 'fix.single': '단일 리뷰', 'fix.synthesis': '패널 종합', 'fix.noTargets': '수정 가능한 문제가 없습니다', 'fix.noTargetsBody': 'HEAD 리뷰를 실행하거나 Reviews에서 문제를 열어 안전한 수정을 준비하세요.', 'fix.job': '작업', 'fix.gitRef': 'Git ref', 'fix.branch': '브랜치', 'fix.targetKind': '대상 유형', 'fix.reviewPreview': '실행 미리보기 확인', 'fix.loadingPreview': '미리보기 불러오는 중…', 'fix.previewReady': '백엔드 미리보기가 준비되었습니다. 아직 명령을 실행하지 않았습니다.', 'fix.previewFailed': '수정을 준비하지 못했습니다: {error}', 'fix.executionPreview': '실행 미리보기', 'fix.confirmTitle': 'RoboRev 수정 확인', 'fix.confirmDescription': '백엔드는 프로세스를 시작하기 전에 이 대상을 다시 검증합니다.', 'fix.repository': '저장소', 'fix.command': '정확한 실행 파일과 argv', 'fix.safety': '이 작업은 파일을 수정하고 커밋을 만들 수 있습니다. 저장소를 깨끗하게 유지하고 완료 후 결과를 확인하세요.', 'fix.startingHead': '시작 HEAD', 'fix.endingHead': '종료 HEAD', 'fix.endingBranch': '종료 브랜치', 'fix.endingDirty': '종료 작업 트리', 'fix.operation': '수정 작업 #{id}', 'fix.starting': '시작 중', 'fix.confirmAction': '확인하고 수정 실행', 'fix.confirming': '시작 중…', 'fix.started': '수정 작업을 시작했습니다.', 'fix.cancel': '수정 취소', 'fix.canceling': '취소 중…', 'fix.cancelRequested': '수정 취소를 요청했습니다.', 'fix.openReview': '결과 리뷰 열기', 'fix.closePreview': '수정 미리보기 닫기', 'error.fixPreviewMalformed': '수정 미리보기 응답 형식이 올바르지 않습니다.', 'error.fixOperationMalformed': '수정 작업 응답 형식이 올바르지 않습니다.',
  'review.panelAria': '리뷰 패널',
  'review.closeDetail': '상세 닫기',
  'review.closeFixPrompt': 'Fix Prompt 닫기',
  'review.tabReview': '리뷰',
  'review.tabLog': '로그',
  'review.tabPrompt': '프롬프트',
  'review.tabComments': '댓글',
  'review.tabMetadata': '메타데이터',
  'panel.aria': 'Panel Refine',
  'panel.run': '실행 #{id}',
  'panel.reviewAt': '{head} 리뷰',
  'panel.fixAt': '수정 #{attempt} · {agent}',
  'panel.pending': '대기 중',
  'panel.notConfigured': '구성되지 않음',
  'panel.notRun': '실행되지 않음',
  'panel.failed': '실패',
  'panel.refreshedError': 'Panel Refine을 갱신하지 못했습니다: {error}',
  'panel.finalReview': '최종 리뷰',
  'panel.needsAttention': '주의 필요',
  'panel.titlePassed': 'Panel Refine 통과',
  'panel.titleNeedsAttention': 'Panel Refine 주의 필요',
  'panel.titleFailed': 'Panel Refine 실패',
  'panel.titleCanceled': 'Panel Refine 취소됨',
  'panel.phaseStarting': '시작 중',
  'panel.phaseReviewing': '리뷰 중',
  'panel.phaseEvaluating': '평가 중',
  'panel.phaseFixing': '수정 중',
  'panel.refineUnavailable': 'Panel Refine 사용 불가: {reason}',
  'panel.unavailableNotInstalled': 'RoboRev가 설치되어 있지 않습니다.',
  'panel.unavailableDaemonOffline': 'RoboRev 데몬이 실행 중이 아닙니다.',
  'panel.branch': '브랜치',
  'panel.resultError': '오류',
  'panel.resultCancelled': '취소됨',
  'setup.backendCommandSupplied': 'RoboRev가 제공한 명령',
  'setup.installWillDo': '정확한 {version} 릴리스를 다운로드하고 전체 SHA-256 체크섬을 확인한 뒤, 검증된 바이너리를 설치·점검하고 RoboRev 데몬을 시작해 확인합니다.',
  'setup.installWillNotDo': 'Windows 보안 정책이나 PATH를 변경하지 않으며, roborev init을 실행하거나 에이전트·훅·확장을 설치하거나 에이전트 키·토큰을 읽거나 저장하지 않습니다.',
  'setup.daemonStartedToast': 'RoboRev 데몬을 시작했습니다.',
  'setup.daemonRestartedToast': 'RoboRev 데몬을 다시 시작했습니다.',
  'setup.versionNotDetected': '감지되지 않음',
  'setup.pathUnavailable': 'PATH를 조회할 수 없음',
  'setup.archiveSizeUnavailable': '백엔드가 제공한 정확한 아카이브 크기',
  'setup.daemonRunningDetail': 'RoboRev 데몬이 실행 중입니다.',
  'setup.daemonNeedsAttention': 'RoboRev 데몬 확인 필요',
  'setup.daemonNeedsAttentionDetail': '데몬은 연결되었지만 상태 또는 버전 확인에 실패했습니다.',
  'setup.daemonHealthFailure': '설치된 데몬이 상태/버전 확인을 통과하지 못했습니다 ({code}).',
  'setup.retryDaemonHealth': '데몬 상태 다시 확인',
  'setup.daemonStoppedDetail': '확인된 설치가 이 Dashboard와 호환되지만 데몬은 중지되어 있습니다.',
  'setup.daemonNotInstalledDetail': 'RoboRev가 설치되어 있지 않습니다.',
  'error.fixPromptMalformed': 'Fix Prompt 응답 형식이 올바르지 않습니다.',
  'error.setupMalformed': 'RoboRev 설정 응답 형식이 올바르지 않습니다.',
  'error.agentsMalformed': '구성된 에이전트 응답 형식이 올바르지 않습니다.',
  'error.runtimeMalformed': '런타임 제어 응답 형식이 올바르지 않습니다.',
  'error.panelConfigurationMalformed': '패널 구성 응답 형식이 올바르지 않습니다.',
  'error.panelRefineMalformed': 'Panel Refine 응답 형식이 올바르지 않습니다.',
  'error.panelRefineNoValidRuns': 'Panel Refine 응답에 유효한 실행이 없습니다.',
  'error.panelRefineWrongRepository': 'Panel Refine 응답이 다른 저장소에 속합니다.',
  'error.daemonOperationTimeout': 'RoboRev 데몬 작업이 제한 시간 안에 종료 상태에 도달하지 않았습니다.',
  'error.reviewTargetMissing': '리뷰가 대기열에 추가되었지만 고정 대상 메타데이터가 없습니다.',
  'error.bootstrapRepositoryMissing': 'bootstrap에 선택한 저장소 메타데이터가 없습니다.',
  'error.repositoryRestoreFailed': '저장소 상태를 복원하지 못했습니다: {error}',
  'panel.versionNoPanel': '이 RoboRev 버전은 패널 리뷰를 지원하지 않습니다.',
  'panel.versionNoFix': '이 RoboRev 버전은 typed 수정 명령을 지원하지 않습니다.',
  'panel.versionNoStatus': '이 RoboRev 버전은 제한된 패널 상태 계약을 지원하지 않습니다.',
  'review.partialSuccess': '부분 성공: {error}',
  'setup.daemonStoppedToast': 'RoboRev 데몬이 중지되었습니다.',
  'setup.upgradeRequired': 'RoboRev 업그레이드 필요',
  'setup.metadataContractUnavailable': '이 버전은 Dashboard에 필요한 안전한 메타데이터 및 대기열 계약을 제공하지 않습니다.',
  'setup.startDaemonHint': '리뷰를 대기열에 추가하고 보려면 데몬을 시작하세요.',
  'setup.configureBinaryHint': '계속하려면 RoboRev 바이너리를 구성하세요.',
  'setup.actionFailed': '설정 작업에 실패했습니다.',
  'setup.healthNotChecked': '확인 안 함',
  'setup.healthPassed': '통과',
  'setup.healthFailed': '실패',
  'setup.healthCanceled': '취소됨',
  'setup.healthSkipped': '건너뜀',
  'setup.closeInstall': '설치 확인 닫기',
  'setup.closeDaemon': '데몬 확인 닫기',
  'setup.closeAgentCheck': '에이전트 확인 닫기',
  'setup.runtime': 'RoboRev 런타임',
  'setup.daemon': '데몬',
  'setup.model': '모델',
  'setup.runInit': '실행',
  'setup.restartDaemonAction': '데몬 다시 시작',
  'setup.stopDaemonAction': '데몬 중지',
  'panel.close': 'Panel Refine 닫기',
  'panel.startAction': 'Panel Refine 시작',
  'analyze.aria': '저장소 분석',
  'analyze.title': '분석',
  'analyze.description': '설치된 RoboRev 버전의 Analyze 유형과 Prompt Template을 확인합니다. Dashboard에서는 실행이 비활성화되어 있습니다.',
  'analyze.type': '분석 유형',
  'analyze.typeDescription': '설치된 RoboRev 버전에서 분석 유형을 검색합니다.',
  'analyze.loadingPrompt': '프롬프트 불러오는 중…',
  'analyze.showPrompt': 'Prompt Template 보기',
  'analyze.promptFailed': 'Prompt Template을 불러오지 못했습니다: {error}',
  'analyze.promptTitle': 'Prompt Template',
  'analyze.copyPrompt': 'Prompt Template 복사',
  'analyze.copyingPrompt': 'Prompt Template 복사 중…',
  'analyze.promptCopied': 'Prompt Template을 복사했습니다.',
  'analyze.promptCopyFailed': 'Prompt Template을 복사하지 못했습니다. 클립보드 접근이 거부되었습니다.',
  'analyze.safety': 'Analyze 실행 안전',
  'analyze.safetyTitle': '실행 비활성화',
  'analyze.agenticWarning': 'RoboRev v0.64 Analyze는 에이전트 작업입니다. 구성된 에이전트가 파일을 수정하고 명령을 실행할 수 있습니다.',
  'analyze.executionDisabled': 'Dashboard가 작업 트리 수명 주기를 안전하게 통제할 수 없어 v0.64 Analyze 실행을 비활성화했습니다. 분석 유형과 Prompt Template 조회는 계속 사용할 수 있습니다.',
  'analyze.executionDisabledTitle': '조회 전용',
  'analyze.executionDisabledCta': '실행 비활성화',
  'analyze.executionDisabledReason': '분석 유형과 Prompt Template을 조회할 수 있습니다.',
  'language.label': '언어', 'language.select': '언어 선택', 'language.ko': '한국어', 'language.en': 'English', 'nav.openMenu': '탐색 메뉴 열기',
  'nav.overview': '개요', 'nav.reviews': '리뷰', 'nav.fixRefine': '수정 및 반복 검증', 'nav.analyze': '분석', 'nav.tasks': '작업', 'nav.repositories': '저장소', 'nav.agentsSetup': '에이전트 및 설정', 'nav.configuration': '구성', 'nav.stats': '통계 및 인사이트', 'nav.advanced': '고급', 'nav.soon': '예정', 'brand.dashboard': '대시보드',
  'stats.aria': '통계 및 인사이트', 'stats.eyebrow': '저장소 분석', 'stats.title': '통계 및 인사이트', 'stats.description': '선택한 저장소의 RoboRev 공식 통계입니다.', 'stats.refresh': '새로고침', 'stats.window': '기간', 'stats.window.24h': '24시간', 'stats.window.7d': '7일', 'stats.window.30d': '30일', 'stats.window.90d': '90일', 'stats.branch': '브랜치 범위', 'stats.branch.all': '모든 브랜치', 'stats.branch.current': '현재 브랜치', 'stats.overview': '개요', 'stats.summary': '리뷰 요약', 'stats.jobsTotal': '작업', 'stats.completed': '완료', 'stats.passed': '통과', 'stats.findings': '문제 발견', 'stats.passRate': '통과율', 'stats.resolutionRate': '해결률', 'stats.addressed': '해결됨', 'stats.durationKind': '시간 유형', 'stats.reviewDuration': '리뷰', 'stats.queueDuration': '대기열', 'stats.failed': '실패', 'stats.errors': '오류', 'stats.median': '중앙값', 'stats.failureTotal': '실패 합계', 'stats.retries': '재시도', 'stats.noVerdict': '판정 없음', 'stats.windowCost': '기간 추정 비용 (하한선)', 'stats.timing': '시간', 'stats.duration': '소요 시간', 'stats.average': '평균', 'stats.p50': 'P50', 'stats.p95': 'P95', 'stats.agents': '에이전트', 'stats.agent': '에이전트', 'stats.jobs': '작업', 'stats.verdicts': '판정', 'stats.jobTypes': '작업 유형', 'stats.failures': '실패 범주', 'stats.cost': '비용', 'stats.costTitle': '추정 비용 범위', 'stats.allTimeCost': '전체 기간 추정 비용 (하한선)', 'stats.jobsWithCost': '비용이 있는 작업', 'stats.costJobsTotal': '비용 작업 합계', 'stats.costIncomplete': '비용 데이터가 불완전합니다. 이 값은 하한선입니다.', 'stats.costCoverage': '무료 사용량은 $0.00로 표시합니다. 비용 범위는 추정치입니다.', 'stats.costCoverageCount': '비용이 보고된 작업: {withCost}/{total}', 'stats.windowCostNote': '기간 비용은 하한선 추정치이며, {withCost}/{total} 작업에 비용이 보고되었습니다.', 'stats.costEmpty': '이 비용 범위에 보고된 작업이 없습니다.', 'stats.insights': '인사이트', 'stats.insightsDisabled': '인사이트는 아직 이 Dashboard에서 활성화되지 않았습니다.', 'stats.loading': '불러오는 중…', 'stats.unavailable': '통계를 사용할 수 없음', 'stats.unavailableBody': '통계에는 실행 중인 데몬과 지원되는 백엔드 기능이 필요합니다.', 'stats.openSetup': '에이전트 및 설정 열기', 'stats.noData': '보고된 데이터가 없습니다.', 'stats.noFailures': '보고된 실패 범주가 없습니다.', 'stats.empty': '이 기간에 보고된 작업이 없습니다.',
  'status.daemonOnline': '데몬 온라인', 'status.daemonOffline': '데몬 오프라인', 'status.setupRequired': '설정 필요', 'status.pass': '통과', 'status.finding': '문제 발견', 'status.done': '완료', 'status.running': '실행 중', 'status.queued': '대기 중', 'status.canceled': '취소됨', 'status.error': '오류', 'status.unknown': '알 수 없음', 'status.unknownAgent': '에이전트 미기록', 'status.open': '열림', 'status.closed': '닫힘', 'status.dirty': '변경 있음', 'status.clean': '깨끗함', 'status.ready': '준비 완료', 'status.stopped': '중지됨', 'status.processing': '처리 중', 'status.paused': '일시중지', 'status.offline': '오프라인',
  'common.cancel': '취소', 'common.close': '닫기', 'common.copy': '복사', 'common.copied': '복사됨', 'common.retry': '다시 시도', 'common.refresh': '새로고침', 'common.loading': '불러오는 중…', 'common.default': '기본값', 'common.defaultModel': '기본 모델', 'common.none': '없음', 'common.yes': '예', 'common.no': '아니요', 'common.chars': '자', 'common.progress': '{phase} 진행률',
  'review.reviewHead': 'HEAD 리뷰', 'review.mode': '리뷰 모드', 'review.selectPanel': '리뷰 패널 선택', 'review.singleAgent': '단일 에이전트 · 패널 없음', 'review.crossReview': '교차 리뷰 · {panel}', 'review.runningCount': '{count}개 실행 중', 'review.operations': '리뷰 작업', 'review.queue': '리뷰 대기열', 'review.liveJobs': '선택한 저장소의 RoboRev 작업입니다. 큰 내용은 필요할 때 불러옵니다.', 'review.search': '리뷰 검색', 'review.searchPlaceholder': '작업, 에이전트, 참조 검색…', 'review.status': '상태', 'review.target': '대상', 'review.reviewer': '리뷰어', 'review.branch': '브랜치', 'review.elapsed': '경과 시간', 'review.state': '상태', 'review.customTask': '사용자 지정 작업', 'review.panelSynthesis': '패널 종합', 'review.title': '리뷰 #{id}', 'review.detail': '리뷰 상세', 'review.detailAria': '리뷰 상세', 'review.detailTabs': '리뷰 상세 탭', 'review.panelMembers': '패널 멤버', 'review.memberProgress': '{terminal} / {total}개 완료', 'review.memberDetail': '패널 멤버 상세', 'review.returnToSynthesis': '패널 종합으로 돌아가기', 'review.memberAgent': '에이전트', 'review.memberModel': '모델', 'review.memberStatus': '상태', 'review.memberVerdict': '판정', 'review.memberDuration': '소요 시간', 'review.noOutput': '아직 리뷰 결과가 없습니다', 'review.runningUpdates': '실행 중인 리뷰는 자동으로 갱신됩니다.', 'review.noMemberJobs': '멤버 작업이 없습니다', 'review.noMemberJobsBody': '단일 에이전트 리뷰이거나 멤버 메타데이터를 사용할 수 없습니다.', 'review.detailRefreshFailed': '리뷰 상세를 갱신하지 못했습니다: {error}', 'review.membersRefreshFailed': '패널 멤버를 갱신하지 못했습니다: {error}', 'review.noContent': '{tab}을(를) 사용할 수 없습니다.', 'review.tabLoadFailed': '{tab}을(를) 불러오지 못했습니다: {error}', 'review.noReviews': '리뷰가 없습니다', 'review.tryDifferentFilter': '다른 필터를 사용해 보세요.', 'review.createHead': 'HEAD 새 리뷰를 만들어 시작하세요.', 'review.comment': '댓글', 'review.commentPlaceholder': '해결 내용을 자세히 붙여넣으세요…', 'review.addComment': '댓글 추가', 'review.commentClose': '댓글 작성 후 닫기', 'review.commentSavedFirst': '댓글을 먼저 저장합니다. 성공한 경우에만 이 리뷰를 닫습니다.', 'review.copyFixPrompt': 'Fix Prompt 복사', 'review.openFixPrompt': 'Fix Prompt 열기', 'review.cancel': '취소', 'review.close': '닫기', 'review.reopen': '다시 열기', 'review.rerun': '같은 대상/구성 다시 실행', 'review.rerunConfirm': '동일한 고정 대상과 확인된 구성을 다시 실행할까요?\n\n실제 RoboRev 전송:\nPOST /api/job/rerun\n{"job_id":{id}}\n\n현재 HEAD는 리뷰하지 않습니다.', 'review.fixPrompt': 'Fix Prompt', 'review.finding': '문제 발견 · 리뷰 #{id}', 'review.fixPromptStale': '이 리뷰 이후 저장소 상태가 변경되었습니다. 사용 전에 프롬프트를 확인하세요.', 'review.fixPromptGenerated': '선택한 리뷰 문제에서 생성되었습니다.', 'review.fixPromptAria': 'Fix Prompt', 'review.fixPromptCopied': '리뷰 #{id}의 Fix Prompt를 복사했습니다.', 'review.clipboardUnavailable': '클립보드를 사용할 수 없습니다. 편집 가능한 Fix Prompt를 계속 열어 둡니다.', 'review.newHeadQueued': '{head}의 새 HEAD 리뷰를 대기열에 추가했습니다.', 'review.stateUpdated': 'RoboRev 상태를 갱신했습니다.', 'review.commentClosed': '댓글을 저장하고 리뷰를 닫았습니다.', 'review.agentCheckCompleted': '{agent} 에이전트 상태 확인을 완료했습니다.', 'review.memberFallback': '멤버 #{id}',
  'tasks.eyebrow': '저장소 작업 공간', 'tasks.title': '작업', 'tasks.description': '선택한 저장소의 기존 RoboRev custom task 작업입니다.', 'tasks.customTask': 'Custom Task', 'tasks.executionDisabledScope': '이 Dashboard 릴리스에서는 실행이 fail-closed로 차단됩니다.', 'tasks.executionDisabledBadge': '실행 비활성화', 'tasks.executionDisabledPlaceholder': 'Custom Task 실행이 비활성화되어 있습니다.', 'tasks.promptLabel': 'Task prompt', 'tasks.executionDisabledReason': 'RoboRev v0.64는 daemon의 effective allow_unsafe_agents / disable_codex_sandbox 상태나 global config identity를 machine-readable 계약으로 제공하지 않으므로, Dashboard는 Custom Task가 Codex read-only 도구로 실행될지 검증할 수 없습니다. job-level non-agentic 기본값은 더 허용적인 daemon 설정으로부터 보호해 주지 않습니다.', 'tasks.executionDisabledHint': 'Task 등록은 이 UI와 백엔드 API 양쪽에서 차단됩니다(task-execution-disabled). 아래의 기존 task 기록과 결과는 계속 사용할 수 있습니다.',   'tasks.history': '최근 실행', 'tasks.historyDescription': '최근 저장소 작업 {limit}개에서 찾은 Custom Task입니다.', 'tasks.noHistory': '최근 작업 {limit}개에서 Custom Task를 찾지 못했습니다.', 'tasks.historyFailed': 'Task 기록을 불러오지 못했습니다: {error}', 'tasks.viewResult': '결과 보기',
  'config.description': 'RoboRev CLI effective config을 조회하고 명시적으로 허용된 설정만 변경합니다. daemon이 실행 중일 필요는 없습니다.', 'config.refresh': '구성 새로고침', 'config.unavailable': '설치된 RoboRev 버전이 제한된 구성 계약을 제공하지 않습니다.', 'config.malformed': 'Dashboard가 RoboRev 구성 응답을 검증하지 못했습니다.', 'config.loadFailed': '구성을 불러오지 못했습니다: {error}', 'config.effectiveTitle': '적용된 구성', 'config.effectiveHint': 'RoboRev CLI effective config가 source of truth입니다. 일부 설정은 RoboRev에 따라 이후 작업에 영향을 주거나 daemon 수명 주기 동작을 필요로 할 수 있습니다.', 'config.columnKey': '키', 'config.columnValue': '값', 'config.origin': '출처', 'config.origin.global': '전역', 'config.origin.local': '저장소', 'config.origin.default': '기본값', 'config.noRows': '보고된 적용 행이 없습니다.', 'config.editable': '편집 가능한 설정', 'config.currentValue': '현재 적용 값', 'config.unset': '(설정 안 됨)', 'config.severityDisabled': '모든 심각도 (필터링 비활성화)', 'config.scope': '범위', 'config.scope.global': '전역 구성', 'config.scope.local': '저장소', 'config.newValue': '새 값', 'config.invalidValue': '허용 범위 안의 값을 입력하세요.', 'config.prepare': '변경 준비', 'config.confirmEyebrow': '실행 확인', 'config.confirmTitle': '구성 변경 확인', 'config.settingLabel': '설정', 'config.command': '명령', 'config.confirmAction': '설정 적용', 'config.warning.local_toml_modified': '.roborev.toml 파일이 수정됩니다.', 'config.warning.tracked_dirty': '이 파일이 tracked 상태면 저장소가 dirty가 됩니다.', 'config.warning.normalize': 'RoboRev가 저장하면서 TOML 형식과 주석을 normalize할 수 있습니다.', 'config.warning.global_normalize': '전역 구성을 저장하면 TOML 형식과 주석이 normalize될 수 있습니다.', 'config.changed': '구성을 변경했습니다.', 'config.dirtyNote': '저장소 구성이 변경되었습니다. .roborev.toml을 직접 검토하고 commit 또는 revert하세요.', 'config.unverified': '설정 변경 결과를 완전히 확인하지 못했습니다. 구성을 새로고침하여 현재 값을 확인하세요.',
  'config.setting.max_workers': '워커 수', 'config.setting.review_context_count': '리뷰 컨텍스트 수', 'config.setting.auto_close_passing_reviews': '통과 리뷰 자동 닫기', 'config.setting.review_reasoning': '리뷰 reasoning', 'config.setting.refine_reasoning': 'Refine reasoning', 'config.setting.review_min_severity': '리뷰 최소 심각도', 'config.setting.fix_min_severity': '수정 최소 심각도', 'config.setting.refine_min_severity': 'Refine 최소 심각도',
  'repo.repository': '저장소', 'repo.currentAria': '현재 저장소', 'repo.registeredTargets': '등록된 대상', 'repo.onlyCanonical': '여기에 등록된 canonical Git 루트만 변경 대상으로 사용할 수 있습니다.', 'repo.addLocal': '로컬 Git 저장소 추가', 'repo.pathPlaceholder': 'E:\\Dev\\GitHub\\project', 'repo.register': '등록', 'repo.trackedEyebrow': 'RoboRev inventory', 'repo.trackedTitle': 'RoboRev 추적 저장소', 'repo.trackedDescription': 'RoboRev daemon이 보고한 조회 전용 저장소 inventory입니다.', 'repo.trackedName': '이름', 'repo.trackedPath': '경로', 'repo.trackedJobs': '작업', 'repo.trackedStatus': 'Dashboard 상태', 'repo.trackedSummary': '추적 저장소 {count}, 작업 {jobs}', 'repo.trackedRegistered': 'Dashboard 등록됨', 'repo.trackedNotRegistered': '미등록', 'repo.trackedRefresh': '새로고침', 'repo.trackedLoading': '추적 저장소 불러오는 중…', 'repo.trackedEmpty': 'RoboRev가 추적하는 저장소가 없습니다.', 'repo.trackedUnavailable': 'RoboRev inventory를 사용할 수 없음', 'repo.trackedUnavailableBody': '추적 저장소를 읽으려면 지원되는 정상 RoboRev daemon이 필요합니다.', 'repo.trackedError': '추적 저장소 inventory를 불러오지 못했습니다.', 'repo.trackedMalformed': 'Dashboard가 RoboRev 저장소 inventory를 검증하지 못했습니다.', 'panel.unavailable': '패널을 사용할 수 없음', 'panel.loading': '리뷰 패널 불러오는 중…', 'panel.configurationUnavailable': '패널 구성을 사용할 수 없음', 'panel.retryDiscovery': '패널 검색 다시 시도',
  'setup.runtimeHealth': '런타임 상태', 'setup.heading': '에이전트 및 설정', 'setup.authNotice': '대시보드는 기존 에이전트 CLI 인증을 사용하며 토큰을 저장하지 않습니다.', 'setup.notInstalled': 'RoboRev가 설치되어 있지 않습니다', 'setup.install': 'RoboRev 설치', 'setup.installStart': '설치 및 시작', 'setup.installConfirm': 'RoboRev를 설치하고 시작할까요?', 'setup.installOnlyStep': '이 단계에서만 소프트웨어를 다운로드하고 설치합니다.', 'setup.explicitConfirmation': '실행 확인', 'setup.executionPreview': '실행 미리보기', 'setup.backendCommand': '백엔드가 제공한 실행 파일과 정확한 typed 인자입니다.', 'setup.repository': '저장소', 'setup.releaseSource': '릴리스 출처', 'setup.version': '버전', 'setup.assetBytes': '파일 크기', 'setup.target': '설치 위치', 'setup.sha256': 'SHA-256', 'setup.compatibility': '호환성', 'setup.officialRelease': '공식 GitHub 릴리스', 'setup.verifiedTarget': '확인된 Dashboard 대상', 'setup.checksumBackend': '백엔드가 제공한 체크섬', 'setup.compatibleWindows': '이 Windows 아키텍처와 호환됩니다.', 'setup.verifyCompatibility': '백엔드가 설치 전에 호환성을 확인합니다.', 'setup.willDo': '수행하는 작업', 'setup.willNotDo': '수행하지 않는 작업', 'setup.installScope': '명시적 설치 범위', 'setup.downloadExact': '정확한 {version} 릴리스를 다운로드합니다.', 'setup.verifyChecksum': '전체 SHA-256 체크섬을 확인합니다.', 'setup.installProbe': '확인된 대상에 설치하고 바이너리를 점검합니다.', 'setup.startDaemonVerify': 'RoboRev 데몬을 시작하고 확인합니다.', 'setup.noSecurityChanges': 'Windows 보안 정책이나 PATH를 변경합니다.', 'setup.noInit': 'roborev init을 실행합니다.', 'setup.noAgents': '에이전트, 훅 또는 확장을 설치합니다.', 'setup.noKeys': '에이전트 키와 토큰을 읽거나 저장합니다.', 'setup.setupUnavailable': '설정 상태를 사용할 수 없음', 'setup.setupResponseInvalid': 'Dashboard가 typed RoboRev 설정 응답을 검증하지 못했습니다.', 'setup.verifiedInstallation': '확인된 설치', 'setup.requestedVersion': '요청한 버전', 'setup.architecture': '아키텍처', 'setup.source': '출처', 'setup.openRelease': '공식 릴리스 출처 열기', 'setup.officialRepository': '공식 저장소', 'setup.asset': '파일', 'setup.pendingInstallation': '설치 대기 중', 'setup.compatibleDashboard': '이 Dashboard와 호환됨', 'setup.notCompatible': '호환되지 않음', 'setup.pendingVerification': '확인 대기 중', 'setup.operationSerialized': '설정 작업은 순서대로 실행됩니다. 백엔드가 준비 완료를 보고할 때까지 리뷰와 변경 작업을 비활성화합니다.', 'setup.failedAction': '설정 작업에 실패했습니다.', 'setup.daemonStopped': 'RoboRev 데몬이 중지됨', 'setup.daemonInstalledStopped': '확인된 RoboRev 바이너리가 설치되어 있습니다. 리뷰를 대기열에 추가하려면 데몬을 시작하세요.', 'setup.daemonStart': '데몬 시작', 'setup.daemonRunning': '데몬 실행 중', 'setup.daemonOffline': '데몬 오프라인', 'setup.daemonReady': 'typed 리뷰 작업을 받을 준비가 되었습니다.', 'setup.daemonNotAccepting': '데몬이 새 작업을 받지 않고 있습니다.', 'setup.daemonControls': '데몬 및 대기열 제어', 'setup.runtimeDescription': 'typed 제어는 데몬 source of truth를 반영합니다. 작업 중에는 동작을 순서대로 처리합니다.', 'setup.runtimeUnavailable': '런타임 제어를 사용할 수 없음', 'setup.runtimeRefresh': 'typed 데몬 및 대기열 상태를 가져오려면 새로고침하세요.', 'setup.reviewQueue': '리뷰 대기열', 'setup.queuePaused': '대기열 일시중지', 'setup.queueProcessing': '대기열 처리 중', 'setup.queueUnknown': '대기열 알 수 없음', 'setup.queuePausedBody': '실행 중인 작업은 계속되며 새 대기 작업은 시작되지 않습니다.', 'setup.queueProcessingBody': '용량이 있으면 대기 작업을 시작할 수 있습니다.', 'setup.queueUnknownBody': '데몬이 오프라인이거나 알 수 없어 대기열 상태를 사용할 수 없습니다.', 'setup.pauseQueue': '대기열 일시중지', 'setup.resumeQueue': '대기열 재개', 'setup.daemonOfflineReason': '데몬이 오프라인입니다.', 'setup.queueUnknownReason': '대기열 상태를 알 수 없습니다. 새로고침하여 다시 시도하세요.', 'setup.controlsDisabled': '제어 비활성화: {reason}', 'setup.panelActive': 'Panel Refine이 실행 중입니다.', 'setup.operationActive': '데몬 작업이 진행 중입니다.', 'setup.configuredAgents': '구성된 리뷰 에이전트', 'setup.agentDescription': '실행 파일/구성 존재 여부를 감지합니다. 상태 확인은 별도이며 명시적으로 확인한 뒤에만 변경됩니다.', 'setup.agentsLoading': '구성된 에이전트 불러오는 중…', 'setup.agentsUnavailable': '구성된 에이전트를 사용할 수 없음', 'setup.agentsRefresh': 'typed 에이전트 구성을 가져오려면 새로고침하세요.', 'setup.noConfiguredAgents': 'RoboRev가 보고한 구성된 리뷰 에이전트가 없습니다.', 'setup.detected': '감지됨', 'setup.health': '상태', 'setup.reasoning': '추론', 'setup.command': '명령', 'setup.sourceLabel': '출처: {source}', 'setup.lastChecked': '마지막 확인 {at}', 'setup.runCheck': '상태 확인', 'setup.agentMustDetected': '상태 확인 전에 에이전트가 감지되어야 합니다.', 'setup.defaultModel': '기본 모델', 'setup.executableDetected': '실행 파일/구성이 감지되었습니다.', 'setup.executableNotDetected': '실행 파일 또는 구성을 감지하지 못했습니다.', 'setup.confirmAgentCheck': '에이전트 상태를 확인할까요?', 'setup.agentCheckDescription': 'RoboRev를 통해 작은 실제 요청을 보내며 subscription/API 할당량을 사용할 수 있습니다.', 'setup.configuredAgent': '구성된 에이전트', 'setup.canonicalAgent': 'canonical 에이전트', 'setup.sendCheck': '선택한 구성 에이전트를 통해서만 상태를 확인합니다.', 'setup.noConfigChange': 'Dashboard의 구성을 변경하거나 자격 증명을 저장하지 않습니다.', 'setup.restartDaemon': 'RoboRev 데몬을 다시 시작할까요?', 'setup.stopDaemon': 'RoboRev 데몬을 중지할까요?', 'setup.restartDescription': '데몬을 중지한 뒤 시작합니다. 실행 중인 작업이 중단될 수 있으며 대기 작업은 유지됩니다.', 'setup.stopDescription': '데몬을 중지합니다. 실행 중인 작업이 중단될 수 있으며 대기 작업은 유지됩니다.', 'setup.requestRestart': 'RoboRev를 통해 typed 데몬 재시작을 한 번 요청합니다.', 'setup.requestStop': 'RoboRev를 통해 typed 데몬 중지를 한 번 요청합니다.', 'setup.noDashboardStop': '리뷰를 삭제하거나 저장소 파일을 변경하거나 Dashboard에서 직접 작업을 중지하지 않습니다.',
  'setup.phase.idle': '대기', 'setup.phase.checking': '확인 중', 'setup.phase.downloading': '다운로드 중', 'setup.phase.extracting': '압축 해제 중', 'setup.phase.verifying': '검증 중', 'setup.phase.installing': '설치 중', 'setup.phase.binding': '연결 중', 'setup.phase.starting_daemon': '데몬 시작 중', 'setup.phase.waiting_for_daemon': '데몬 확인 중', 'setup.phase.ready': '준비 완료', 'setup.phase.failed': '실패', 'setup.phase.blocked': '차단됨', 'setup.phase.canceled': '취소됨', 'setup.phase.unsupported': '지원되지 않음', 'setup.setupTitle': 'RoboRev 설정 · {phase}', 'setup.setupWaiting': 'Dashboard가 확인된 RoboRev 설치가 완료되기를 기다리고 있습니다.', 'setup.blockedTitle': 'RoboRev가 Windows Application Control에 의해 차단됨', 'setup.blockedBody': '확인된 파일을 시작하지 못했습니다. Windows Security 또는 조직의 application-control 정책을 확인하세요. Dashboard는 이를 비활성화하거나 우회할 수 없습니다.', 'setup.retryVerification': '검증 다시 시도', 'setup.unsupportedTitle': 'RoboRev {version}은(는) 지원되지 않음', 'setup.unsupportedBody': 'Dashboard가 자동으로 다운그레이드하지 않습니다. 권장 버전: {version}.', 'setup.installRejected': 'RoboRev 설치가 거부됨', 'setup.setupFailed': 'RoboRev 설정 실패', 'setup.failureCode': '실패 코드: {code}.', 'setup.operationIncomplete': 'typed 설정 작업이 완료되지 않았습니다.', 'setup.retryInstallation': '설치 다시 시도', 'setup.retryStart': '시작 다시 시도', 'setup.setupCanceled': 'RoboRev 설정이 취소됨', 'setup.canceledBody': '부분 설치를 준비 완료로 취급하지 않습니다. 필요할 때 새 검증 설정 작업을 시작하세요.', 'setup.retrySetup': '설정 다시 시도', 'setup.installRelease': '리뷰와 Panel Refine을 사용하려면 확인된 {version} 릴리스를 설치하세요.', 'setup.downloaded': '{bytes} 다운로드됨', 'setup.bytesProgress': '{percent}% · {downloaded} / {total}', 'setup.progress': '{phase} 진행률',
  'panel.automatedGate': '자동화된 품질 게이트', 'panel.description': '선택한 패널 리뷰를 반복하고 Codex 수정을 적용한 뒤 새 HEAD를 검증합니다.', 'panel.openReview': '리뷰 열기', 'panel.totalReviews': '전체 리뷰', 'panel.fixAttempts': '수정 시도', 'panel.iterationPanel': '반복 패널', 'panel.finalPanel': '최종 패널', 'panel.finalResult': '최종 패널 결과', 'panel.fixAgent': '수정 에이전트', 'panel.startedHead': '시작 HEAD', 'panel.currentHead': '현재 HEAD', 'panel.elapsed': '경과 시간', 'panel.currentReview': '현재 리뷰', 'panel.none': '없음', 'panel.cancelState': '취소 후 저장소 상태', 'panel.branchStateUnavailable': 'HEAD / 브랜치 상태를 사용할 수 없음', 'panel.dirtyTree': '변경된 작업 트리', 'panel.cleanTree': '깨끗한 작업 트리', 'panel.warningFinalState': '경고: 최종 저장소 상태를 확인하지 못했습니다. 계속하기 전에 작업 트리를 점검하세요.', 'panel.warningPartialChanges': '경고: 취소로 인해 저장소에 일부 변경이 남았을 수 있습니다. 계속하기 전에 작업 트리를 점검하세요.', 'panel.timeline': '타임라인', 'panel.openFinalReview': '최종 리뷰 열기', 'panel.openLatestReview': '최신 리뷰 열기', 'panel.cancel': 'Panel Refine 취소', 'panel.start': 'Panel Refine 시작', 'panel.dialogDescription': 'Panel Refine은 선택한 패널로 현재 HEAD를 반복 리뷰하고, 실패한 종합 리뷰를 사용해 수정을 만든 뒤 커밋합니다. 통과하거나 한도에 도달할 때까지 새 HEAD를 리뷰합니다.', 'panel.maxAttempts': '최대 수정 시도', 'panel.default': ' (기본값)', 'panel.finalDeep': '최종 심층 패널', 'panel.preview': '실행 미리보기', 'panel.optionsLoading': '백엔드 명령 옵션 불러오는 중…', 'panel.previewDescription': '서버 측 수정 adapter는 백엔드가 제공한 실행 파일과 정확한 typed argv를 사용합니다. 브라우저에서 shell 명령이나 실행 파일을 받지 않습니다.', 'panel.started': 'Panel Refine을 시작했습니다.', 'panel.cancelRequested': 'Panel Refine 취소를 요청했습니다.',
  'error.analyzeCatalogMalformed': 'RoboRev 분석 catalog를 검증하지 못했습니다.',
  'error.analyzePromptMalformed': 'RoboRev Prompt Template을 검증하지 못했습니다.',
  'error.dashboardFailed': 'Dashboard UI 오류', 'error.reload': '새로고침', 'error.backgroundRefresh': '백그라운드 새로고침 문제', 'roadmap.title': '체크포인트 로드맵', 'roadmap.body': '이 typed workflow는 ROBoreV-COVERAGE.md에서 추적하며 generic shell endpoint 없이 추가합니다.',
}

ko['stats.costIncomplete'] = '일부 적격 작업에 비용이 보고되지 않아 실제 사용액은 이 하한선보다 높을 수 있습니다.'
ko['stats.costEmpty'] = '이 범위에 비용을 보고할 수 있는 적격 작업이 아직 없습니다.'
ko['stats.insightsDisabled'] = '이 체크포인트에서는 Guideline Insights가 활성화되지 않았습니다.'
ko['stats.guidelineTitle'] = 'Guideline Insights'
ko['stats.guidelineDescription'] = '최근 실패 리뷰와 현재 guideline을 분석해 반복 패턴과 읽기 전용 개선안을 제안합니다.'
ko['stats.guidelineQuota'] = 'LLM 할당량 또는 provider 비용이 사용될 수 있습니다.'
ko['stats.guidelineNoConfig'] = '추천만 제공하며 구성이나 .roborev.toml은 변경하지 않습니다.'
ko['stats.guidelineWarningUnknown'] = '추가 Insights 안전 경고가 적용됩니다.'
ko['stats.guidelinePrepare'] = 'Insights 준비'
ko['stats.guidelineRun'] = 'Insights 실행'
ko['stats.guidelineConfirmEyebrow'] = '명시적 확인'
ko['stats.guidelineConfirmTitle'] = 'Guideline Insights 실행 확인'
ko['stats.guidelineExecutable'] = '실행 파일'
ko['stats.guidelineArguments'] = '인자 배열'
ko['stats.guidelineState.queued'] = '대기 중'
ko['stats.guidelineState.running'] = '실행 중'
ko['stats.guidelineState.done'] = '완료'
ko['stats.guidelineState.failed'] = '실패'
ko['stats.guidelineState.canceled'] = '취소됨'
ko['stats.guidelineState.skipped'] = '건너뜀'
ko['stats.guidelineState.unverified'] = '확인되지 않음'
ko['stats.guidelineAmbiguous'] = '실행 결과를 확인하지 못했습니다. RoboRev Insights 작업이 이미 대기열에 추가되었을 수 있습니다. 자동 재시도하지 말고 필요하면 새 실행을 준비하세요.'
ko['stats.guidelineRepository'] = '저장소'
ko['stats.guidelineWindow'] = 'Insights 기간'
ko['stats.guidelineBranch'] = 'Insights 브랜치 범위'
ko['stats.guidelineAgent'] = '에이전트 구성'
ko['stats.guidelineDefaultAgent'] = 'RoboRev에 구성된 기본 agent/model/reasoning 사용'
ko['stats.guidelineDisclosure'] = '분석을 위해 최근 실패 리뷰 output, 메타데이터, comments, addressed 상태 및 resolved review guidelines가 RoboRev로 전송될 수 있습니다.'
ko['stats.guidelineLifetime'] = '페이지를 닫아도 daemon 작업은 계속됩니다. 이 페이지는 현재 실행만 추적합니다.'
ko['stats.guidelineSkipped'] = '이 기간에 실패 리뷰가 없습니다. LLM 작업을 실행하지 않았습니다.'
ko['stats.guidelineUnverified'] = '실행 결과를 확인하지 못했습니다. RoboRev Insights 작업이 이미 대기열에 추가되었을 수 있습니다. 자동 재시도하지 말고 필요하면 새 실행을 준비하세요.'
ko['stats.guidelineJobRunning'] = 'Insights 작업 #{id}이(가) {status} 상태입니다.'
ko['stats.guidelineReceipt'] = '현재 Insights 작업: #{id}. daemon은 이 페이지와 독립적으로 계속 실행됩니다.'
ko['stats.insightsMalformed'] = 'Dashboard가 Insights 응답을 검증하지 못했습니다.'
ko['stats.guidelineResultFailed'] = 'Insights 결과를 불러오지 못했습니다. daemon을 확인한 뒤 다시 시도하세요.'
ko['stats.guidelineRetryResult'] = '결과 다시 시도'
ko['stats.insightsCopyFailed'] = '클립보드에 접근할 수 없습니다.'
ko['repo.rename'] = '이름 변경'
ko['repo.renameTitle'] = 'RoboRev 저장소 이름 변경'
ko['repo.renameDescription'] = 'RoboRev 표시 이름만 변경합니다. 파일 시스템 경로와 Dashboard 등록은 유지됩니다.'
ko['repo.renameCurrentName'] = '현재 이름'
ko['repo.renameNewName'] = '새 표시 이름'
ko['repo.renamePath'] = '추적 루트 경로'
ko['repo.renameJobs'] = '작업'
ko['repo.renameDashboardStatus'] = 'Dashboard 상태'
ko['repo.renameCommand'] = '정확한 실행 파일과 argv'
ko['repo.renameWarning'] = '표시 이름만 변경합니다. 파일 시스템 이동, Git 작업 또는 Dashboard 등록 변경은 하지 않습니다.'
ko['repo.renamePrepare'] = '이름 변경 준비'
ko['repo.renamePreparing'] = '준비 중…'
ko['repo.renameUnavailable'] = '현재 RoboRev를 사용할 수 없습니다. 다시 사용 가능해지면 새 Rename Preview를 준비하세요.'
ko['repo.renameUnsupported'] = '설치된 RoboRev는 저장소 이름 변경을 지원하지 않습니다.'
ko['repo.renameConfirm'] = '이름 변경 확인'
ko['repo.renameConfirming'] = '이름 변경 중…'
ko['repo.renameSuccess'] = '저장소 표시 이름을 변경했습니다.'
ko['repo.renameUnverified'] = '이름 변경 프로세스는 시작했지만 새 이름을 inventory에서 확인하지 못했습니다. 새로고침하여 현재 상태를 확인하세요. 자동 재시도하지 않았습니다.'
ko['repo.renameFailed'] = '저장소 이름을 변경하지 못했습니다: {error}'
ko['repo.renameMalformed'] = 'Dashboard가 저장소 이름 변경 응답을 검증하지 못했습니다.'
ko['repo.renameInvalidEmpty'] = '비어 있지 않은 표시 이름을 입력하세요.'
ko['repo.renameInvalidTooLong'] = '표시 이름은 Unicode 문자 128자 이하여야 합니다.'
ko['repo.renameInvalidUtf8'] = '올바른 Unicode 텍스트를 입력하세요.'
ko['repo.renameInvalidControl'] = '제어 문자는 사용할 수 없습니다.'
ko['repo.renameInvalidLeadingDash'] = '“-”로 시작하는 이름은 사용할 수 없습니다.'
ko['repo.renameInvalidSame'] = '현재와 다른 표시 이름을 입력하세요.'
ko['repo.renameClose'] = '이름 변경 대화상자 닫기'
ko['repo.move'] = '경로 복구'
ko['repo.moveTitle'] = 'RoboRev 저장소 경로 복구'
ko['repo.moveDescription'] = 'RoboRev의 오래된 경로 기록을 Dashboard에 이미 등록된 저장소에 다시 연결합니다.'
ko['repo.moveSource'] = '오래된 추적 저장소'
ko['repo.moveSourcePath'] = '오래된 추적 경로'
ko['repo.moveJobs'] = '작업'
ko['repo.moveStatus'] = 'Dashboard 상태'
ko['repo.moveDestination'] = '등록된 대상'
ko['repo.moveDestinationPlaceholder'] = '등록된 저장소를 선택하세요'
ko['repo.moveDestinationPath'] = '대상 경로'
ko['repo.moveDestinationName'] = '대상 이름'
ko['repo.moveWorkingDirectory'] = '작업 디렉터리'
ko['repo.moveCommand'] = '정확한 실행 파일, 작업 디렉터리 및 argv'
ko['repo.moveWarning'] = '폴더를 이동하지 않습니다. 이미 이동한 저장소를 RoboRev 기록의 새 경로에 다시 연결합니다.'
ko['repo.moveRegistrationWarning'] = '새 경로는 먼저 Dashboard에 등록되어 있어야 합니다.'
ko['repo.movePrepare'] = '경로 복구 준비'
ko['repo.movePreparing'] = '준비 중…'
ko['repo.moveUnavailable'] = '현재 RoboRev를 사용할 수 없습니다. 다시 사용 가능해지면 새 Move Preview를 준비하세요.'
ko['repo.moveUnsupported'] = '설치된 RoboRev는 저장소 경로 복구를 지원하지 않습니다.'
ko['repo.moveConfirm'] = '경로 복구 확인'
ko['repo.moveConfirming'] = '이동 중…'
ko['repo.moveSuccess'] = 'RoboRev 저장소 경로를 복구했습니다.'
ko['repo.moveUnverified'] = '경로 복구 프로세스는 시작했지만 새 경로를 inventory에서 확인하지 못했습니다. 새로고침하여 현재 상태를 확인하세요. 자동 재시도하지 않았습니다.'
ko['repo.moveMalformed'] = 'Dashboard가 저장소 경로 복구 응답을 검증하지 못했습니다.'
ko['repo.moveClose'] = '저장소 경로 복구 대화상자 닫기'
ko['repo.merge'] = '병합'
ko['repo.mergeTitle'] = '오래된 RoboRev 저장소 항목 병합'
ko['repo.mergeDescription'] = '오래된 RoboRev source 항목을 이 Dashboard에 이미 등록된 저장소로 통합합니다.'
ko['repo.mergeSource'] = '오래된 source'
ko['repo.mergeSourcePath'] = 'source 경로'
ko['repo.mergeSourceJobs'] = 'source 작업'
ko['repo.mergeTarget'] = '등록된 target'
ko['repo.mergeTargetPlaceholder'] = '등록된 target 선택'
ko['repo.mergeTargetName'] = 'target 이름'
ko['repo.mergeTargetPath'] = 'target 경로'
ko['repo.mergeTargetJobs'] = 'target 작업'
ko['repo.mergeCommand'] = '정확한 실행 파일, working directory 및 argv'
ko['repo.mergeWorkingDirectory'] = 'Working directory'
ko['repo.mergeWarning'] = '오래된 RoboRev source 항목을 제거하고 작업, 리뷰 및 관련 메타데이터를 target으로 통합합니다.'
ko['repo.mergeWarningNoRollback'] = 'target 이름/경로는 유지됩니다. 파일 시스템, Git, Dashboard Registry를 변경하지 않으며 자동 rollback/retry를 하지 않습니다.'
ko['repo.mergePrepare'] = '병합 준비'
ko['repo.mergePreparing'] = '준비 중…'
ko['repo.mergeUnavailable'] = '현재 RoboRev를 사용할 수 없습니다. 다시 사용 가능해지면 새 Merge Preview를 준비하세요.'
ko['repo.mergeUnsupported'] = '설치된 RoboRev는 저장소 병합을 지원하지 않습니다.'
ko['repo.mergeConfirm'] = '병합 확인'
ko['repo.mergeConfirming'] = '병합 중…'
ko['repo.mergeSuccess'] = '오래된 RoboRev 저장소 항목을 병합했습니다.'
ko['repo.mergeUnverified'] = '병합 프로세스는 시작했지만 새 RoboRev inventory에서 통합 상태를 확인하지 못했습니다. 새로고침하여 현재 상태를 확인하세요. 자동 재시도하지 않았습니다.'
ko['repo.mergeMalformed'] = 'Dashboard가 저장소 병합 응답을 검증하지 못했습니다.'
ko['repo.mergeClose'] = '저장소 병합 대화상자 닫기'
ko['repo.mergeEligibility'] = '병합 확인'
ko['repo.mergeEligibilityChecking'] = '병합 가능 여부 확인 중…'
ko['repo.mergeEligibilitySourceExists'] = '이 source 경로가 디스크에 아직 존재하므로 오래된 항목이 아닙니다.'
ko['repo.mergeEligibilityRegistered'] = '이 저장소는 이미 Dashboard에 등록되어 있습니다.'
ko['repo.mergeEligibilityEmpty'] = '이 오래된 항목에는 병합할 작업 기록이 없습니다.'
ko['repo.mergeEligibilityActive'] = '대기 중이거나 실행 중인 작업이 있어 안전하게 병합할 수 없습니다.'
ko['repo.mergeEligibilityNoTargets'] = '등록되어 있고 RoboRev가 추적 중인 target이 없습니다.'
ko['repo.mergeEligibilityFailed'] = '병합 가능 여부를 확인하지 못했습니다.'
ko['repo.mergeEligibilityMalformed'] = 'Dashboard가 병합 가능 여부 응답을 검증하지 못했습니다.'
ko['repo.moveEligibility'] = '경로 복구 확인'
ko['repo.moveEligibilityChecking'] = '경로 복구 확인 중…'
ko['repo.moveEligibilitySourceExists'] = '이 추적 경로가 디스크에 아직 존재하므로 경로 복구가 필요하지 않습니다.'
ko['repo.moveEligibilityRegistered'] = '이 저장소는 이미 Dashboard에 등록되어 있습니다.'
ko['repo.moveEligibilityFailed'] = '경로 복구 가능 여부를 확인하지 못했습니다.'
ko['repo.moveEligibilityMalformed'] = 'Dashboard가 경로 복구 가능 여부 응답을 검증하지 못했습니다.'
ko['diagnostics.title'] = '런타임 진단'
ko['diagnostics.description'] = '데몬, 워커, 대기열, 작업 및 상태를 읽기 전용으로 확인합니다.'
ko['diagnostics.open'] = '진단'
ko['diagnostics.collapse'] = '진단 숨기기'
ko['diagnostics.refresh'] = '진단 새로고침'
ko['diagnostics.loading'] = '런타임 진단 불러오는 중…'
ko['diagnostics.unavailable'] = '사용 불가 / 오프라인'
ko['diagnostics.unavailableBody'] = '현재 런타임 진단을 사용할 수 없습니다.'
ko['diagnostics.retry'] = '진단 다시 시도'
ko['diagnostics.daemon'] = '데몬'
ko['diagnostics.workers'] = '워커'
ko['diagnostics.queue'] = '대기열'
ko['diagnostics.jobs'] = '작업'
ko['diagnostics.health'] = '상태'
ko['diagnostics.running'] = '실행 중'
ko['diagnostics.offline'] = '오프라인'
ko['diagnostics.version'] = '버전'
ko['diagnostics.uptime'] = '가동 시간'
ko['diagnostics.activeMax'] = '활성 / 최대'
ko['diagnostics.paused'] = '일시중지'
ko['diagnostics.processing'] = '처리 중'
ko['diagnostics.queued'] = '대기 중'
ko['diagnostics.runningJobs'] = '실행 중인 작업'
ko['diagnostics.completed'] = '완료'
ko['diagnostics.failed'] = '실패'
ko['diagnostics.skipped'] = '건너뜀'
ko['diagnostics.healthy'] = '정상'
ko['diagnostics.degraded'] = '저하됨'
ko['diagnostics.errors24h'] = '오류 (24시간)'
ko['diagnostics.components'] = '구성 요소'
ko['diagnostics.componentHealthy'] = '정상'
ko['diagnostics.componentUnhealthy'] = '비정상'

const catalogs: Record<Locale, Record<MessageKey, string>> = { en, ko }

function interpolate(template: string, values?: MessageValues) {
  if (!values) return template
  return template.replace(/\{(\w+)\}/g, (_, key: string) => Object.prototype.hasOwnProperty.call(values, key) ? String(values[key]) : `{${key}}`)
}

function browserLocale(): Locale {
  if (typeof navigator !== 'undefined' && /^ko(?:-|$)/i.test(navigator.language || '')) return 'ko'
  return 'en'
}

function readStoredLocale(): Locale | undefined {
  try {
    if (typeof window === 'undefined') return undefined
    const value = window.localStorage.getItem(LANGUAGE_STORAGE_KEY)
    return value === 'ko' || value === 'en' ? value : undefined
  } catch { return undefined }
}

function writeStoredLocale(locale: Locale) {
  try { if (typeof window !== 'undefined') window.localStorage.setItem(LANGUAGE_STORAGE_KEY, locale) } catch { /* private/denied storage is non-fatal */ }
}

export function resolveLocale(): Locale { return readStoredLocale() ?? browserLocale() }

type I18nContextValue = {
  locale: Locale
  setLocale: (locale: Locale) => void
  t: <K extends MessageKey>(key: K, values?: MessageValues) => string
  format: <K extends MessageKey>(key: K, values?: MessageValues) => string
}

const fallbackLocaleRef = { current: 'en' as Locale }
const fallbackT = <K extends MessageKey>(key: K, values?: MessageValues) => interpolate(catalogs[fallbackLocaleRef.current][key] ?? en[key], values)
const I18nContext = createContext<I18nContextValue>({ locale: 'en', setLocale: locale => { fallbackLocaleRef.current = locale; writeStoredLocale(locale) }, t: fallbackT, format: fallbackT })

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(resolveLocale)
  const localeRef = useRef(locale)
  localeRef.current = locale
  const setLocale = useCallback((next: Locale) => { localeRef.current = next; setLocaleState(next); writeStoredLocale(next) }, [])
  const t = useCallback(<K extends MessageKey>(key: K, values?: MessageValues) => interpolate(catalogs[localeRef.current][key] ?? en[key], values), [])
  const format = useCallback(<K extends MessageKey>(key: K, values?: MessageValues) => interpolate(catalogs[localeRef.current][key] ?? en[key], values), [])
  useEffect(() => { if (typeof document !== 'undefined') document.documentElement.lang = locale }, [locale])
  const value = useMemo(() => ({ locale, setLocale, t, format }), [locale, setLocale, t, format])
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}

export function useI18n() { return useContext(I18nContext) }

export function LanguageSelector({ compact = false }: { compact?: boolean }) {
  const { locale, setLocale, t } = useI18n()
  return <label className={`language-selector ${compact ? 'compact' : ''}`}>
    <Globe size={compact ? 14 : 16} aria-hidden="true" />
    <span>{t('language.label')}</span>
    <select aria-label={t('language.select')} value={locale} onChange={event => setLocale(event.target.value as Locale)}>
      <option value="ko">{t('language.ko')}</option>
      <option value="en">{t('language.en')}</option>
    </select>
  </label>
}

export { I18nContext }
