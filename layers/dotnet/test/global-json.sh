#!/usr/bin/env bash
# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Usage: global-json.sh FILE SDK_VERSION
#
# Checks upstream's global.json at FILE against SDK_VERSION, the SDK this
# stage's image reports, then rewrites FILE so that the SDK it names is a
# floor rather than the one feature band allowed. The Containerfile's
# backend stage runs it over its checkout as
# global-json.sh global.json "$(cd / && dotnet --version)": from /, no
# global.json is in the way, so dotnet reports the image's own SDK.
#
# The check: global.json's sdk.version and SDK_VERSION must share a major
# and minor. When upstream moves to a new .NET first, the image's SDK and
# runtime must move with it, approved on the Dependency Dashboard; when
# the image moves first, it waits for upstream. Each fails here with its
# own steps to take, rather than later with dotnet's own error, which
# says nothing of what to do.
#
# The rewrite: upstream's sdk.version stays, with "rollForward":
# "latestFeature", which takes any later band of the same major and minor.
# A global.json with no policy accepts its own band alone, so once
# Renovate moved the SDK image to a later band the build would refuse to
# start, and could deadlock with the app's own update; a policy upstream
# sets is replaced. This is the one change the image's build makes to an
# upstream file, and it never leaves the backend stage. A source with no
# global.json is built as it is, by the image's SDK, and runtime-major.sh,
# after publish, still guards what ships.
#
# Its limits: the rewrite keeps sdk.version alone, and drops every other
# key, such as msbuild-sdks, and the sdk object's allowPrerelease and
# paths, so an upstream that needs one needs this script changed first. A
# global.json with no sdk.version, one that pins msbuild-sdks alone among
# them, is refused, not rewritten. And only FILE is read, the global.json
# at the source's root, as the backend stage names it: one further down
# the tree is neither checked nor rewritten.
#
# test/globaljson runs it over fixtures, and checks each way it can
# refuse.
#
# Exit status: 0 when FILE is rewritten, printing what it now holds, or
# when there is no FILE, saying so; 1 when SDK_VERSION is not an SDK
# version, when FILE has no sdk.version, or one that is not an SDK
# version, or when the two differ in major or minor, with an ERROR line on
# stderr and FILE as it was; 2 when the arguments are not two, or FILE is
# there but is not a regular file.
set -euo pipefail

if (($# != 2)) || { [[ ! -f "${1}" ]] && [[ -e "${1}" || -L "${1}" ]]; }; then
	echo "usage: ${0} FILE SDK_VERSION, where FILE is upstream's global.json, if it has one, and SDK_VERSION the SDK this stage's image reports" >&2
	exit 2
fi
FILE="${1}"
SDK_VERSION="${2}"

# A release's version, or a preview's, such as 11.0.100-rc.2.25502.107
sdk_re='^([0-9]+)[.]([0-9]+)[.][0-9]+(-[0-9A-Za-z.]+)?$'
if [[ ! "${SDK_VERSION}" =~ ${sdk_re} ]]; then
	echo "ERROR: this stage's SDK is '${SDK_VERSION}', not an SDK version, so ${FILE} cannot be checked against it." >&2
	exit 1
fi
# Base 10, which a leading zero would otherwise turn to octal
have_major=$((10#${BASH_REMATCH[1]}))
have_minor=$((10#${BASH_REMATCH[2]}))

if [[ ! -e "${FILE}" ]]; then
	echo "no global.json at ${FILE}: the image's SDK builds the source as it is"
	exit 0
fi
json=$(<"${FILE}")

# sdk.version, a JSON string. The sdk object holds no object of its own,
# so the first closing brace after its opening one ends it.
re='"sdk"[[:space:]]*:[[:space:]]*[{][^}]*"version"[[:space:]]*:[[:space:]]*"([^"]*)"'
if [[ ! "${json}" =~ ${re} ]]; then
	echo "ERROR: ${FILE} has no sdk.version to keep as the floor." >&2
	exit 1
fi
version="${BASH_REMATCH[1]}"
if [[ ! "${version}" =~ ${sdk_re} ]]; then
	echo "ERROR: ${FILE}'s sdk.version is '${version}', not an SDK version." >&2
	exit 1
fi
want_major=$((10#${BASH_REMATCH[1]}))
want_minor=$((10#${BASH_REMATCH[2]}))

# The major first, then the minor, each compared as a number
want="${want_major}.${want_minor}"
have="${have_major}.${have_minor}"
if ((want_major > have_major || (want_major == have_major && want_minor > have_minor))); then
	echo "ERROR: global.json asks for the .NET ${want} SDK; this stage's image has .NET ${have}. Approve the .NET ${want} major on the Dependency Dashboard and move both together: CLAUDE.md, 'A .NET major'." >&2
	exit 1
elif [[ "${want}" != "${have}" ]]; then
	echo "ERROR: global.json asks for the .NET ${want} SDK; this stage's image has .NET ${have}. A .NET major waits until upstream retargets, then moves with the app's own update: CLAUDE.md, 'A .NET major'." >&2
	exit 1
fi

printf '{"sdk":{"version":"%s","rollForward":"latestFeature"}}\n' "${version}" >"${FILE}"
cat "${FILE}"
