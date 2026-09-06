#!/usr/bin/env bash
set -euo pipefail

version=${1:?Usage: release.sh VERSION RELEASE_NAME NOTES_FILE}
name=${2:?Provide a descriptive release name}
notes=${3:?Provide a release notes file}
[[ $version =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || { echo 'Expected stable SemVer without v prefix' >&2; exit 1; }
[[ -s $notes ]] || { echo 'Release notes must exist and be nonempty' >&2; exit 1; }
[[ $(git branch --show-current) == main ]] || { echo 'Release from main only' >&2; exit 1; }
[[ -z $(git status --porcelain) ]] || { echo 'Commit all changes before releasing' >&2; exit 1; }
tag="v$version"
remote_tags=$(git ls-remote --tags origin "refs/tags/$tag" "refs/tags/$tag^{}")
releases=$(gh release list --limit 1000 --json tagName --jq '.[].tagName')
if git show-ref --verify --quiet "refs/tags/$tag" || [[ -n $remote_tags ]] || [[ $'\n'$releases$'\n' == *$'\n'"$tag"$'\n'* ]]; then
  echo "Tag/release $tag already exists; never move published tags. Investigate manually." >&2
  exit 1
fi
python3 - "$version" <<'PY'
import pathlib, sys
old = tuple(map(int, pathlib.Path('VERSION').read_text().strip().split('.')))
new = tuple(map(int, sys.argv[1].split('.')))
if new < old:
    raise SystemExit('Version cannot decrease')
PY
if [[ $(cat VERSION) != "$version" ]]; then
  printf '%s\n' "$version" > VERSION
  git add VERSION
  git commit -m "Release $tag: $name"
fi
commit=$(git rev-parse HEAD)
git push origin HEAD:refs/heads/main
# Wait for the push-triggered workflow for this exact commit to become visible.
run_id=''
for ((attempt=0; attempt<60; attempt++)); do
  run_id=$(gh run list --workflow ci.yml --branch main --commit "$commit" --event push --limit 20 --json databaseId --jq '.[0].databaseId // empty')
  [[ -n $run_id ]] && break
  sleep 5
done
[[ -n $run_id ]] || { echo 'No CI run found; no tag created' >&2; exit 1; }
gh run watch "$run_id" --exit-status
result=$(gh run view "$run_id" --json headSha,conclusion --jq '.headSha + " " + .conclusion')
[[ $result == "$commit success" ]] || { echo 'Exact release commit did not pass CI' >&2; exit 1; }
remote_main=$(git ls-remote origin refs/heads/main)
[[ ${remote_main%%$'\t'*} == "$commit" ]] || { echo 'Remote main advanced; refusing stale release' >&2; exit 1; }
[[ $(git rev-parse HEAD) == "$commit" && -z $(git status --porcelain) ]] || { echo 'Working tree changed during CI' >&2; exit 1; }
git tag -a "$tag" "$commit" -m "$tag — $name"
git push origin "refs/tags/$tag"
gh release create "$tag" --verify-tag --latest --title "$tag — $name" --notes-file "$notes"
gh release view "$tag" --json tagName,isDraft,isPrerelease,url,targetCommitish
