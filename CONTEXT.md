# Commit Review

git-review tracks user review of changes on a feature branch.

## Language

**Stack**:
An original commit and its immediately following fixup, amend, or reword commits.
Approval applies to the reviewed stack content, even when its commit identities change.

**Stack content**:
The combined change from the original commit's parent to the final commit in the stack.
Commit metadata and intermediate commit boundaries are not part of this content.
A fixup! commit carries no message, so patch identity alone decides its review status.
Appending an amend! or reword! commit still creates a new stack version that requires review, even when the combined change is unchanged.

**Attempt**:
One recorded review or explicit approval of an exact stack.
An interrupted attempt does not imply approval.

**Change**:
A logical change that can have successive stack versions.
An optional Change-Id connects these versions without transferring approval.

**Feedback**:
Comments or session notes returned by a review.
Changed stack content supersedes the previous result but does not prove that its feedback was addressed.
