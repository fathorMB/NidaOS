# Functions for the GitHub part of the VM test (milestone M-05). vm-test.sh
# reads this file with "source".
#
# The test uses a GitHub App and a private repository that the operator
# made (knowledge/testing.md):
#   NIDA_TEST_GITHUB_APP_ID    the ID of the app
#   NIDA_TEST_GITHUB_APP_KEY   the private key of the app (PEM)
#   NIDA_TEST_GITHUB_REPO      the private repository, "owner/name"
# The app must be installed on the repository.

GH_API=${GH_API:-https://api.github.com}

gh_configured() {
    [ -n "${NIDA_TEST_GITHUB_APP_ID:-}" ] && [ -n "${NIDA_TEST_GITHUB_APP_KEY:-}" ] && [ -n "${NIDA_TEST_GITHUB_REPO:-}" ]
}

b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }

# gh_key_file writes the private key into a file with the mode 0600.
gh_key_file() {
    local f=$PWD/github-app.pem
    if [ ! -s "$f" ]; then
        (umask 077 && printf '%s\n' "$NIDA_TEST_GITHUB_APP_KEY" > "$f")
    fi
    echo "$f"
}

gh_jwt() {
    local now header payload sig key
    key=$(gh_key_file)
    now=$(date +%s)
    header=$(printf '{"alg":"RS256","typ":"JWT"}' | b64url)
    payload=$(printf '{"iat":%d,"exp":%d,"iss":"%s"}' $((now - 60)) $((now + 540)) "$NIDA_TEST_GITHUB_APP_ID" | b64url)
    sig=$(printf '%s.%s' "$header" "$payload" | openssl dgst -sha256 -sign "$key" | b64url)
    printf '%s.%s.%s' "$header" "$payload" "$sig"
}

# gh_token gives an installation token for the test repository. It keeps
# the token for 30 minutes.
GH_TOKEN_VALUE=
GH_TOKEN_TIME=0
gh_token() {
    local now jwt inst
    now=$(date +%s)
    if [ -z "$GH_TOKEN_VALUE" ] || [ $((now - GH_TOKEN_TIME)) -gt 1800 ]; then
        jwt=$(gh_jwt)
        inst=$(curl -fsS -H "Authorization: Bearer $jwt" -H "Accept: application/vnd.github+json" \
            "$GH_API/repos/$NIDA_TEST_GITHUB_REPO/installation" | jq -r .id) || return 1
        GH_TOKEN_VALUE=$(curl -fsS -X POST -H "Authorization: Bearer $jwt" -H "Accept: application/vnd.github+json" \
            "$GH_API/app/installations/$inst/access_tokens" | jq -r .token) || return 1
        GH_TOKEN_TIME=$now
    fi
    printf '%s' "$GH_TOKEN_VALUE"
}

# gh_api METHOD PATH [JSON] calls the API for the test repository. PATH starts
# after /repos/owner/name.
gh_api() {
    local method=$1 path=$2 data=${3:-}
    local tok
    tok=$(gh_token) || return 1
    if [ -n "$data" ]; then
        curl -fsS -X "$method" -H "Authorization: Bearer $tok" -H "Accept: application/vnd.github+json" \
            "$GH_API/repos/$NIDA_TEST_GITHUB_REPO$path" -d "$data"
    else
        curl -fsS -X "$method" -H "Authorization: Bearer $tok" -H "Accept: application/vnd.github+json" \
            "$GH_API/repos/$NIDA_TEST_GITHUB_REPO$path"
    fi
}

# gh_put_file BRANCH PATH FILE MESSAGE adds or replaces a file on a branch.
gh_put_file() {
    local branch=$1 path=$2 file=$3 msg=$4 sha body
    sha=$(gh_api GET "/contents/$path?ref=$branch" 2>/dev/null | jq -r '.sha // empty') || true
    body=$(jq -n --arg m "$msg" --arg c "$(base64 -w0 < "$file")" --arg b "$branch" --arg s "$sha" \
        '{message: $m, content: $c, branch: $b} + (if $s == "" then {} else {sha: $s} end)')
    gh_api PUT "/contents/$path" "$body" > /dev/null
}

# gh_new_branch BRANCH makes a branch from the default branch.
gh_new_branch() {
    local base sha
    base=$(gh_api GET "" | jq -r .default_branch) &&
        sha=$(gh_api GET "/git/ref/heads/$base" | jq -r .object.sha) &&
        gh_api POST /git/refs "$(jq -n --arg r "refs/heads/$1" --arg s "$sha" '{ref: $r, sha: $s}')" > /dev/null
}

gh_delete_branch() {
    gh_api DELETE "/git/refs/heads/$1" > /dev/null 2>&1 || true
}

# gh_clean_old removes the branches of the test that are older than one
# day: a run that stopped leaves them.
gh_clean_old() {
    local limit b date
    limit=$(date -d '1 day ago' +%s)
    for b in $(gh_api GET "/branches?per_page=100" | jq -r '.[].name | select(startswith("nidaos-test-") or startswith("nidaos/deploy-ci-"))'); do
        date=$(gh_api GET "/commits/$b" | jq -r .commit.committer.date)
        if [ "$(date -d "$date" +%s)" -lt "$limit" ]; then
            gh_delete_branch "$b"
        fi
    done
}
