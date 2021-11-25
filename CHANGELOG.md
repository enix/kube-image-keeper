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
