# Changelog

## [3.0.0](https://github.com/zetlen/falconet/compare/v2.0.0...v3.0.0) (2026-09-18)


### ⚠ BREAKING CHANGES

* **commit:** `falconet commit` refuses changes to CI, automation and scanner configuration whatever `paths.allow` says. Name a path in `paths.allow_dangerous_access_to` to exempt it.

### Features

* **commit:** run the guards in their own job and refuse changes to CI configuration ([#56](https://github.com/zetlen/falconet/issues/56)) ([eb36010](https://github.com/zetlen/falconet/commit/eb3601053af6f5b5b675836cb8b5cb48f3fd0914))

## [2.0.0](https://github.com/zetlen/falconet/compare/v1.1.2...v2.0.0) (2026-09-17)


### ⚠ BREAKING CHANGES

* **prepare:** start a run only from a sender with write access ([#49](https://github.com/zetlen/falconet/issues/49))

### Features

* **gitea:** a Gitea adapter over net/http ([#51](https://github.com/zetlen/falconet/issues/51)) ([faec526](https://github.com/zetlen/falconet/commit/faec526459146bc373db84cc896190d15d72ad0e))
* **gitea:** choose the forge from the config and read Gitea's events ([#53](https://github.com/zetlen/falconet/issues/53)) ([90480f5](https://github.com/zetlen/falconet/commit/90480f5a7b9476a71144463cfbe1aec564165ac4))
* **prepare:** start a run only from a sender with write access ([#49](https://github.com/zetlen/falconet/issues/49)) ([87c5840](https://github.com/zetlen/falconet/commit/87c5840b130086510974b0d07c04990531d263e8))
* publish release binaries and install them at a tag ([#43](https://github.com/zetlen/falconet/issues/43)) ([4721c40](https://github.com/zetlen/falconet/commit/4721c40ad2f01f4c730d25916058ae0a753b55ad))
* readable live agent output and a run summary on the run page ([#52](https://github.com/zetlen/falconet/issues/52)) ([6ce5052](https://github.com/zetlen/falconet/commit/6ce5052693b9079f5115967b65348312ed33a2f7))


### Bug Fixes

* **config:** pin the model in the default harness command ([#45](https://github.com/zetlen/falconet/issues/45)) ([e91e60f](https://github.com/zetlen/falconet/commit/e91e60f2d2eadc115cafa363297bc417744741ff)), closes [#1](https://github.com/zetlen/falconet/issues/1)
* **make:** keep a git hook's repository out of every recipe ([#46](https://github.com/zetlen/falconet/issues/46)) ([916fce7](https://github.com/zetlen/falconet/commit/916fce76d8a9ae70cdb5d2309ebda563e24c943d))
* **pause:** put the blocking label on before the hand-over comment ([#44](https://github.com/zetlen/falconet/issues/44)) ([78d018a](https://github.com/zetlen/falconet/commit/78d018aab6144ad9b717ef7f63f1cfa0d2ac7448)), closes [#31](https://github.com/zetlen/falconet/issues/31)
* **release:** open the release pull request with a token that may write a workflow ([#54](https://github.com/zetlen/falconet/issues/54)) ([302b32a](https://github.com/zetlen/falconet/commit/302b32a2ec1371e0d648b030785bec7ad65e4458))
