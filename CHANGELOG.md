## [1.4.2](https://gitlab.enix.io/products/docker-cache-registry/compare/v1.4.1...v1.4.2) (2022-12-06)


### Bug Fixes

* **controllers:** fix pod count ([b1c0d86](https://gitlab.enix.io/products/docker-cache-registry/commit/b1c0d8610d8a23eda487d1ba713ae00d10013a66)), closes [#35](https://gitlab.enix.io/products/docker-cache-registry/issues/35)
* **helm:** Add default tolerations for proxy service ([a1b26b2](https://gitlab.enix.io/products/docker-cache-registry/commit/a1b26b264aba60ed9bded013e0bb30cf3eabea76))
* **helm:** Use correct controller labels for service selector ([4c7479a](https://gitlab.enix.io/products/docker-cache-registry/commit/4c7479af4b496132ddf29bdd0a78d3d4de0175db))

## [1.4.1](https://gitlab.enix.io/products/docker-cache-registry/compare/v1.4.0...v1.4.1) (2022-12-01)


### Bug Fixes

* **controllers:** add -latest to CachedImage names when tag is missing ([0b9b9b4](https://gitlab.enix.io/products/docker-cache-registry/commit/0b9b9b43c0625936c9df3de5451b22bc071f1078))
* **controllers:** hash repository labels longer than 63 chars ([d797e09](https://gitlab.enix.io/products/docker-cache-registry/commit/d797e091b3311a1bb8aa4cc6c60be2606a71924a)), closes [#33](https://gitlab.enix.io/products/docker-cache-registry/issues/33)
* **proxy:** allow variadic components in images names ([611f29d](https://gitlab.enix.io/products/docker-cache-registry/commit/611f29d3ec83599e5176e4f5a14545a4cb722a8f)), closes [#32](https://gitlab.enix.io/products/docker-cache-registry/issues/32)
* keep capital letters in sanitized names and make them lowercase ([212326b](https://gitlab.enix.io/products/docker-cache-registry/commit/212326b3d7945a094629600c6df4cdea3ce6790d))

# [1.4.0](https://gitlab.enix.io/products/docker-cache-registry/compare/v1.3.1...v1.4.0) (2022-11-24)


### Bug Fixes

* **controllers:** check for 404 when checking image existence ([db9b3ee](https://gitlab.enix.io/products/docker-cache-registry/commit/db9b3ee201964fcf0f05a88d32f01ad1bdb5e71c))
* **controllers:** don't trigger image caching again after deleting it ([dd90834](https://gitlab.enix.io/products/docker-cache-registry/commit/dd90834482d6db15166d4887cbde989375663fa2))
* **webhook:** prevent pod creation if webhook fails ([f2e3e02](https://gitlab.enix.io/products/docker-cache-registry/commit/f2e3e02eed55166447f31b36e37392061daa9a3a))


### Features

* **controllers:** CachedImage events ([e3b5e12](https://gitlab.enix.io/products/docker-cache-registry/commit/e3b5e127711cf3be4d4a27e98f074eb4cf57978f)), closes [#28](https://gitlab.enix.io/products/docker-cache-registry/issues/28)
* **crd:** add isCached in CachedImage additionalPrinterColumns ([032b527](https://gitlab.enix.io/products/docker-cache-registry/commit/032b527b52944a2dc6c0c824ea5f0df67426976e))
* **crd:** improve CachedImage status and columns ([f6d82a4](https://gitlab.enix.io/products/docker-cache-registry/commit/f6d82a4f278897a0b55b16dd34b0f5bcf46cfb52)), closes [#29](https://gitlab.enix.io/products/docker-cache-registry/issues/29)

## [1.3.1](https://gitlab.enix.io/products/docker-cache-registry/compare/v1.3.0...v1.3.1) (2022-10-19)


### Bug Fixes

* **helm:** support for k8s 1.20 ([2afa898](https://gitlab.enix.io/products/docker-cache-registry/commit/2afa89836468814231a6db4d59e1ba9581d561ea))
* **proxy:** handle authentication when proxying registry ([dec04ea](https://gitlab.enix.io/products/docker-cache-registry/commit/dec04ea9d57630b9852caf5fe214b114c5305dd5)), closes [#23](https://gitlab.enix.io/products/docker-cache-registry/issues/23)
* **proxy:** suppress errors on cancel pull ([73bb2a8](https://gitlab.enix.io/products/docker-cache-registry/commit/73bb2a8d620b9db27655cf87f022f87d23427581))

# [1.3.0](https://gitlab.enix.io/products/docker-cache-registry/compare/v1.2.1...v1.3.0) (2022-10-07)


### Bug Fixes

* **ci:** Helm jobs trigger ([9880881](https://gitlab.enix.io/products/docker-cache-registry/commit/9880881dbcd624d82734e9d9bed21e1d7a0414d3))
* **helm:** fix webhook object selector value usage ([70c12b5](https://gitlab.enix.io/products/docker-cache-registry/commit/70c12b5d5784f2ef0cf7fc420c80ab0b23968f20))
* **proxy:** fallback to origin registry when cache registry is down ([b438cd1](https://gitlab.enix.io/products/docker-cache-registry/commit/b438cd185ae7b0bbf66b0a13de7a5bb5488e7761))


### Features

* **cache:** garbage collection ([1b5dfd9](https://gitlab.enix.io/products/docker-cache-registry/commit/1b5dfd9095a9dff480aae73db458fa4e0f258061)), closes [#12](https://gitlab.enix.io/products/docker-cache-registry/issues/12)

## [1.2.1](https://gitlab.enix.io/products/docker-cache-registry/compare/v1.2.0...v1.2.1) (2022-09-27)


### Bug Fixes

* **controller:** filter out non-rewritten pods in controllers ([20e0552](https://gitlab.enix.io/products/docker-cache-registry/commit/20e0552ecab687766b129d95cd9f71eb41b9b4f3))
* **controller:** fix missing init containers in CachedImages + tests ([582ded8](https://gitlab.enix.io/products/docker-cache-registry/commit/582ded8c8b1d5ef868099d77abcf246b19f11928))
* **controller:** prevent update "empty" Pods on CachedImage deleted ([070be56](https://gitlab.enix.io/products/docker-cache-registry/commit/070be56f4a88bb8b6643152ad308bcef686b326e))
* **webhook:** enable reinvocationPolicy to handle added containers too ([6c45769](https://gitlab.enix.io/products/docker-cache-registry/commit/6c45769801d8344bb393fdfca8e099fff4889bd1))
* **webhook:** map original images to containers based on container name ([6667d62](https://gitlab.enix.io/products/docker-cache-registry/commit/6667d62635f178aaa843e9036c240edf96160900))

# [1.2.0](https://gitlab.enix.io/products/docker-cache-registry/compare/v1.1.3...v1.2.0) (2022-01-25)


### Bug Fixes

* **cache:** use the right port when proxy.hostPort is set in values.yaml ([27f69b6](https://gitlab.enix.io/products/docker-cache-registry/commit/27f69b6b6796972fc338d68d0764b8686cc6c532)), closes [#18](https://gitlab.enix.io/products/docker-cache-registry/issues/18)
* **proxy:** fix pull from cache ([d10d375](https://gitlab.enix.io/products/docker-cache-registry/commit/d10d37570d03c550bebbc108e1b0cbf1acd8572e))


### Features

* **cache:** authenticate requests to private registries ([00fcc48](https://gitlab.enix.io/products/docker-cache-registry/commit/00fcc4870d2c3c39a0911123002b410e69427bd5))

## [1.1.3](https://gitlab.enix.io/products/docker-cache-registry/compare/v1.1.2...v1.1.3) (2022-01-06)


### Bug Fixes

* **proxy:** fix calls without authentication ([da78ca8](https://gitlab.enix.io/products/docker-cache-registry/commit/da78ca8b17fdecc75fc0cb399ac507ed2bd080c4))
* **proxy:** proxy requests to index.docker.io instead of docker.io ([4e0e24b](https://gitlab.enix.io/products/docker-cache-registry/commit/4e0e24b0b100a9ea767e78f0f28243bea2d6ca91))
* **proxy:** register routes before starting the server ([0edd4d2](https://gitlab.enix.io/products/docker-cache-registry/commit/0edd4d2985f4722fe4b1a069691e1865f16cd1cc))

## [1.1.2](https://gitlab.enix.io/products/docker-cache-registry/compare/v1.1.1...v1.1.2) (2022-01-06)


### Bug Fixes

* **ci:** Avoid duplicate pipeline on MR ([0f1e1b8](https://gitlab.enix.io/products/docker-cache-registry/commit/0f1e1b8bee62400b687597d313c5a63b0208eff2))
* **helm:** fix templating when .matchExpressions array is empty ([7b6bae1](https://gitlab.enix.io/products/docker-cache-registry/commit/7b6bae1dd67796ec460d74b8ebd20b52ca0e4d99))

## [1.1.1](https://gitlab.enix.io/products/docker-cache-registry/compare/v1.1.0...v1.1.1) (2021-11-25)


### Bug Fixes

* **webhook:** prefix images from standard library with "library/" ([ff44d1d](https://gitlab.enix.io/products/docker-cache-registry/commit/ff44d1d294055db2f150ba4e161537717150b5b4)), closes [#16](https://gitlab.enix.io/products/docker-cache-registry/issues/16)

# [1.1.0](https://gitlab.enix.io/products/docker-cache-registry/compare/v1.0.0...v1.1.0) (2021-11-24)


### Bug Fixes

* **helm:** RBAC templates ([c324e48](https://gitlab.enix.io/products/docker-cache-registry/commit/c324e48ede1c47393a02b48d76d740a51db48e7b))
* **helm:** Templating syntax ([3715596](https://gitlab.enix.io/products/docker-cache-registry/commit/3715596b6abec0bd0aa649170062887500a1c6b3))
* **helm:** Volume size ([f48300b](https://gitlab.enix.io/products/docker-cache-registry/commit/f48300bd9e580f8fe7a2f6a390b3bfb9bfc9ab8c))
* **proxy:** fix HTTP 400 when proxying to origin registry ([a11111c](https://gitlab.enix.io/products/docker-cache-registry/commit/a11111cd40260f2fe88fcb83899f7818ea02a8e6))
* **proxy:** fix image url for non-default repositories ([8ba6619](https://gitlab.enix.io/products/docker-cache-registry/commit/8ba6619def7c4202898fe8874de48f507fc2a850))
* **proxy:** fix proxying non-default registries ([0a4dded](https://gitlab.enix.io/products/docker-cache-registry/commit/0a4dded3e0d902669003624f3e943cef349c1683))
* **webhook:** ignore failures and filter out pods from cache registry ([77a2cd4](https://gitlab.enix.io/products/docker-cache-registry/commit/77a2cd411bdac0decb879d73cdb61363988dc01e)), closes [#15](https://gitlab.enix.io/products/docker-cache-registry/issues/15)


### Features

* **helm:** add optional registry UI ([5b37111](https://gitlab.enix.io/products/docker-cache-registry/commit/5b371117ddc38f0b84eaadd6f2f6a8b2cc6cdd4e))
* **helm:** Add PSP support ([ebc1ca1](https://gitlab.enix.io/products/docker-cache-registry/commit/ebc1ca1b7e6cfb7833e9e9ff4dbfac7c7e0e9334))
* **helm:** install CachedImage CRD by default ([364f8ad](https://gitlab.enix.io/products/docker-cache-registry/commit/364f8ad782af3114265f95c09ca469d21ed39ac7))
* **helm:** Rework Helm chart ([9935146](https://gitlab.enix.io/products/docker-cache-registry/commit/9935146af7fd04f42c1c0c11c5d64c7ab9fdaf0a))
* **image-rewriter:** rewrite images ([26e4b0c](https://gitlab.enix.io/products/docker-cache-registry/commit/26e4b0ca20675225f1a6cce7ed8b8e806a346f99))

# 1.0.0 (2021-10-01)


### Bug Fixes

* **cache:** fix pod watching for in-cluster config ([bc1533c](https://gitlab.enix.io/products/docker-cache-registry/commit/bc1533c77b75ae6e2b963b0e6bd627b8c5677163))
* **manager:** force ownership on server-side apply ([067dcbe](https://gitlab.enix.io/products/docker-cache-registry/commit/067dcbee5734c1e37059d3f174a85b88a7b95a15))
* **proxy:** get scope from headers for authentication ([c2a876c](https://gitlab.enix.io/products/docker-cache-registry/commit/c2a876c07644c93302dda6137d32b43f5cb8a2f0))
* **proxy:** url rewriting before proxying ([f2f8c66](https://gitlab.enix.io/products/docker-cache-registry/commit/f2f8c66aae0720b0769f63a7d29b60e661b690ea))


### Features

* **cache:** cache and delete images ([0c65769](https://gitlab.enix.io/products/docker-cache-registry/commit/0c657699144878540269492a855bf2eb531e1ba0))
* **cache:** watch images used by pods ([b545a66](https://gitlab.enix.io/products/docker-cache-registry/commit/b545a661d4910243690901771bc9a2f87973b26b))
* **cache:** watch pods by node ([0cc6541](https://gitlab.enix.io/products/docker-cache-registry/commit/0cc6541cd143f9f65b0150ddd13ed735a6085fd0))
* **helm:** add option to make cache persistent ([ea5900a](https://gitlab.enix.io/products/docker-cache-registry/commit/ea5900a6232821c2fa66554d4304dfe95e704311))
* **helm:** add verbosity and registry env in values.yaml ([e5b1f95](https://gitlab.enix.io/products/docker-cache-registry/commit/e5b1f958887c5058d3dba68bf9c9fa6cb39aae32))
* **helm:** helm chart ([7e04928](https://gitlab.enix.io/products/docker-cache-registry/commit/7e04928807fa770260c3d87f2237131e38d8d61e))
* **helm:** make cache accessible only from within the cluster ([0063ffc](https://gitlab.enix.io/products/docker-cache-registry/commit/0063ffc5319d087b4a4221f1360072f396b04c6a))
* **manager:** add CachedImage.Status.IsCached ([32bda91](https://gitlab.enix.io/products/docker-cache-registry/commit/32bda910c53e3fd6a9cd2db6bbfbb0ac40f984aa))
* **manager:** cachedimage finalizer ([0d9fc5c](https://gitlab.enix.io/products/docker-cache-registry/commit/0d9fc5cb049c35efe215bd8ba7af7de18ab4f2e2))
* **manager:** delete expired cached images ([6be1703](https://gitlab.enix.io/products/docker-cache-registry/commit/6be170301fbab930cce473dc90e80e0f2b1393b0))
* **manager:** flag expiry-delay ([865ccb0](https://gitlab.enix.io/products/docker-cache-registry/commit/865ccb0a93f07e4e19296a7757fead9f6dbeb4a8))
* **manager:** leader election ([3aff6d6](https://gitlab.enix.io/products/docker-cache-registry/commit/3aff6d603c47b1bb664bc77543a90602a58bccf2))
* **manager:** reconcile Pods on CachedImage deleted ([fa68ee8](https://gitlab.enix.io/products/docker-cache-registry/commit/fa68ee86218f707a28e6efdb0539c95a042623df))
* **manager:** set expiresAt for unused cachedimages ([63b701a](https://gitlab.enix.io/products/docker-cache-registry/commit/63b701a94cf3b638840f5f7284e39258d9f6c2c3))
* **proxy:** proxy requests to index.docker.io or cache registry ([7a261f0](https://gitlab.enix.io/products/docker-cache-registry/commit/7a261f0eff6c7b1c47a820d90a6151a81e8380ed))
* **proxy:** replace *docker.io by index.docker.io ([3625d2d](https://gitlab.enix.io/products/docker-cache-registry/commit/3625d2d1cab7498e49b4b4a3f109341d04b57412))
* **proxy:** support non-default registries ([11edf67](https://gitlab.enix.io/products/docker-cache-registry/commit/11edf670d21d6bcb29a8f96cd8c986aea849874f))
* support caching specific tags ([3491c16](https://gitlab.enix.io/products/docker-cache-registry/commit/3491c168f1533dc7511733ec53ffc7f0c8290fed))
