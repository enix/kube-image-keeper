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
