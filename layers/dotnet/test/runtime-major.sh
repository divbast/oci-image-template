#!/usr/bin/env bash
# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Usage: runtime-major.sh DIR RUNTIME_IMAGE
#
# Checks the app published to DIR targets the .NET of RUNTIME_IMAGE, the
# image the runtime stage is built FROM. Each DIR/*.runtimeconfig.json
# names the frameworks the app runs on, under framework or frameworks, and
# the major and minor of every one's version must be those leading
# RUNTIME_IMAGE's tag. The Containerfile's backend stage runs it as
# runtime-major.sh /out/bin "${RUNTIME_IMAGE}" once the app is published.
#
# What it guards: an app built for one .NET does not start on another's
# runtime, and nothing in the build would say so. global-json.sh holds the
# SDK to the major and minor global.json names, but an upstream that pins
# no SDK is built by whichever SDK the image has, and Renovate moves the
# SDK and runtime images together. This check reads what ships. When the
# app moves to a new .NET first, the runtime image must move with it,
# approved on the Dependency Dashboard; when the runtime image moves
# first, it waits for the app. Each fails here with its own steps to take.
#
# The tag is the part of RUNTIME_IMAGE after its last /, up to any @, after
# its :, so a registry's port is never read as one; a digest alone, or a
# tag such as latest, carries no version and is refused. A runtimeconfig
# with includedFrameworks is a self-contained app's, carrying a runtime of
# its own, and is refused whatever else it names.
#
# test/runtimemajor runs it over fixtures, and checks each way it can
# refuse.
#
# Exit status: 0 when every framework's version and the tag share a major
# and minor, printing them; 1 when DIR holds no runtimeconfig, or one is
# self-contained, lacks a framework version or names one that is not a
# version, when the versions disagree, when the tag starts with no version,
# or when the app's major or minor is not the tag's, with an ERROR line on
# stderr; 2 when the arguments are not two, or DIR is not a directory.
set -euo pipefail

if (($# != 2)) || [[ ! -d "${1}" ]]; then
	echo "usage: ${0} DIR RUNTIME_IMAGE, where DIR holds the published app and RUNTIME_IMAGE is the image it runs on" >&2
	exit 2
fi
DIR="${1}"
RUNTIME_IMAGE="${2}"

shopt -s nullglob
configs=("${DIR}"/*.runtimeconfig.json)
if ((${#configs[@]} == 0)); then
	echo "ERROR: ${DIR} holds no *.runtimeconfig.json, so nothing says which .NET the app targets." >&2
	exit 1
fi

# The objects naming a framework: the framework object, and each object in
# the frameworks array. Neither holds an object or array of its own, so
# the first closing brace ends an object, and the first closing bracket
# the array.
one='"framework"[[:space:]]*:[[:space:]]*([{][^}]*[}])'
many='"frameworks"[[:space:]]*:[[:space:]]*[[]([^]]*)[]]'
object='[{][^}]*[}]'
version_re='"version"[[:space:]]*:[[:space:]]*"([^"]*)"'
# A release's version, or a preview's, such as 11.0.0-rc.2.25502.107
dotnet_re='^([0-9]+)[.]([0-9]+)[.][0-9]+(-[0-9A-Za-z.]+)?$'

app="" first=""
for config in "${configs[@]}"; do
	json=$(<"${config}")
	# Refused before any version is read, so none listed under it can be
	# taken for the app's
	if [[ "${json}" == *'"includedFrameworks"'* ]]; then
		echo "ERROR: ${config} lists includedFrameworks: the app was published self-contained, with a runtime of its own rather than the runtime image's." >&2
		exit 1
	fi
	frameworks=()
	if [[ "${json}" =~ ${one} ]]; then
		frameworks+=("${BASH_REMATCH[1]}")
	fi
	if [[ "${json}" =~ ${many} ]]; then
		rest="${BASH_REMATCH[1]}"
		while [[ "${rest}" =~ ${object} ]]; do
			frameworks+=("${BASH_REMATCH[0]}")
			rest="${rest#*"${BASH_REMATCH[0]}"}"
		done
	fi
	if ((${#frameworks[@]} == 0)); then
		echo "ERROR: ${config} has no framework version: it names no framework under framework or frameworks." >&2
		exit 1
	fi
	for framework in "${frameworks[@]}"; do
		if [[ ! "${framework}" =~ ${version_re} ]]; then
			echo "ERROR: ${config} has no framework version for one of the frameworks it names." >&2
			exit 1
		fi
		version="${BASH_REMATCH[1]}"
		if [[ ! "${version}" =~ ${dotnet_re} ]]; then
			echo "ERROR: ${config} names framework version '${version}', not a .NET version." >&2
			exit 1
		fi
		# Base 10, which a leading zero would otherwise turn to octal
		major=$((10#${BASH_REMATCH[1]}))
		minor=$((10#${BASH_REMATCH[2]}))
		if [[ -z "${app}" ]]; then
			app="${major}.${minor}" app_major="${major}" app_minor="${minor}" first="${config}"
		elif [[ "${major}.${minor}" != "${app}" ]]; then
			echo "ERROR: the app's frameworks disagree on its .NET: ${first} names ${app}, and ${config} names ${major}.${minor}." >&2
			exit 1
		fi
	done
done

# The tag: after the last /, which a registry's port comes before, and up
# to any @, which a digest comes after
name="${RUNTIME_IMAGE##*/}"
name="${name%%@*}"
tag=""
if [[ "${name}" == *:* ]]; then
	tag="${name#*:}"
fi
tag_re='^([0-9]+)[.]([0-9]+)([.-]|$)'
if [[ ! "${tag}" =~ ${tag_re} ]]; then
	echo "ERROR: ${RUNTIME_IMAGE} has no .NET version leading its tag, so the app cannot be checked against it." >&2
	exit 1
fi
image_major=$((10#${BASH_REMATCH[1]}))
image_minor=$((10#${BASH_REMATCH[2]}))
runtime="${image_major}.${image_minor}"

# The major first, then the minor, each compared as a number
if ((app_major > image_major || (app_major == image_major && app_minor > image_minor))); then
	echo "ERROR: the app targets .NET ${app}; the runtime image is .NET ${runtime}. Approve the .NET ${app} major on the Dependency Dashboard and move both together: CLAUDE.md, 'A .NET major'." >&2
	exit 1
elif [[ "${app}" != "${runtime}" ]]; then
	echo "ERROR: the app targets .NET ${app}; the runtime image is .NET ${runtime}. A .NET major waits until the app retargets, then moves with the app's own update: CLAUDE.md, 'A .NET major'." >&2
	exit 1
fi
echo "app and runtime image on .NET ${app}"
