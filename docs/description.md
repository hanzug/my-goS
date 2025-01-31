### kafka

**原始数据处理**



在kafka方面，我们的数据来源是爬虫爬取网页数据到kafka中，在topic raw。然后经过我们的流处理器进行处理，转换为csv格式，放入topic csv。



然后工作就来到我们的索引构建模块，这个模块和用户是分离的。索引构建模块的工作是读取kafka中的csv格式的文档数据，一个是通过使用mapreduce分布式计算框架，来构建对应的倒排索引，第二个是构建我们的TrieTree用于词条联想。这些数据落库后通过主从数据库同步到位于搜索引擎模块的从数据库。



### etcd

**服务注册**

首先我们的微服务在启动的时候，把自己的地址注册到etcd中，并申请一个**租约**，客户端定时更新租约，确保存活。

一共注册了三个服务：用户模块，收藏夹模块，搜索引擎模块。



### 网关

**服务发现**

grpc使用etcd解析器，通过服务名从etcd中得到对应的服务，这里可以使用grpc提供的负载均衡策略。在之后watch监听etcd中服务地址变化，并且更新到本地的地址中。

> 在gRPC客户端配置中，服务的目标地址通常会指定一个URI，这个URI包括解析器的方案和服务的名称。例如，`etcd:///my-service`指出使用`etcd`解析器来解析名为`my-service`的服务。当gRPC框架解析这个URI时，它会查找并调用对应方案的解析器的`Build`方法，创建解析器实例。
>
> **解析器的具体职责**
>
> - **初始化解析器状态**：在`Build`函数中，解析器会初始化其内部状态，这包括设置与gRPC框架的连接（`ClientConn`）和构建服务的键前缀（`keyPrefix`），这个键前缀通常用于在服务注册中心（如etcd）中查询服务地址。
> - **启动服务发现**：解析器会调用`start`方法开始服务发现的过程，这通常涉及到监听服务注册中心的变化，如新服务实例的注册或现有实例的注销。





**用户操作**



要使用我们的搜索功能，首先我们的用户请求会来到网关部分，网关部分在初始化的时候使用了rpc+etcd resolver进行服务发现。

然后使用gin框架对请求url进行解析，定位到对应的处理函数，执行相关的rpc请求到搜索引擎模块，搜索引擎对搜索字符串进行分词操作。查询缓存/数据库中对应的倒排索引的bitmap，进行按位并，得到相关的文档。

对所有的文档进行打分之后返回客户端结果。