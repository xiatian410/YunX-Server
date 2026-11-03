package com.yunx.app.data.network

import okhttp3.*
import okio.Buffer
import okio.ForwardingSource
import okio.buffer
import java.io.IOException
import java.net.*
import java.util.concurrent.TimeUnit

/** Headless runtime: no system proxy, no automatic retry, bounded calls, public addresses only. */
object ProviderDeadline { val current = ThreadLocal<Long>() }

object HttpClients {
    @Volatile internal var testClient: OkHttpClient? = null
    private val allowPrivate = System.getenv("YUNX_ALLOW_PRIVATE_DOWNLOADS") == "true"
    fun publicAddress(ip: InetAddress): Boolean {
        if (ip.isAnyLocalAddress || ip.isLoopbackAddress || ip.isLinkLocalAddress || ip.isSiteLocalAddress || ip.isMulticastAddress) return false
        val b = ip.address.map { it.toInt() and 255 }
        return if (b.size == 16) b[0] and 0xfe != 0xfc else !(b[0] == 0 || b[0] == 127 || b[0] >= 224 || b[0] == 100 && b[1] in 64..127 || b[0] == 198 && b[1] in 18..19)
    }
    private val client = OkHttpClient.Builder()
        .proxy(Proxy.NO_PROXY)
        .dns(object : Dns { override fun lookup(hostname: String): List<InetAddress> = Dns.SYSTEM.lookup(hostname).also { ips -> if (!allowPrivate && ips.any { !publicAddress(it) }) throw UnknownHostException("Private destination blocked") } })
        .connectTimeout(10, TimeUnit.SECONDS).readTimeout(25, TimeUnit.SECONDS)
        .writeTimeout(25, TimeUnit.SECONDS).callTimeout(45, TimeUnit.SECONDS)
        .retryOnConnectionFailure(false)
        .connectionPool(ConnectionPool(8, 60, TimeUnit.SECONDS))
        .addInterceptor { chain ->
            val remaining = ProviderDeadline.current.get()?.minus(System.nanoTime())?.div(1_000_000)?.coerceAtMost(45000)?.toInt() ?: 45000
            if(remaining <= 0) throw java.net.SocketTimeoutException("Provider deadline exceeded")
            chain.withConnectTimeout(minOf(10000,remaining),TimeUnit.MILLISECONDS).withReadTimeout(minOf(25000,remaining),TimeUnit.MILLISECONDS).withWriteTimeout(minOf(25000,remaining),TimeUnit.MILLISECONDS).proceed(chain.request())
        }
        .addNetworkInterceptor { chain ->
            val original = chain.call().request().url
            val request = chain.request()
            if (request.url.scheme !in listOf("http", "https") || request.url.username.isNotEmpty() || request.url.password.isNotEmpty()) throw IOException("Invalid remote URL")
            val safe = if (request.url.host != original.host) request.newBuilder().removeHeader("Cookie").removeHeader("Authorization").build() else request
            val response=chain.proceed(safe)
            val body=response.body
            if(body==null) response else {
                val limited=object:ForwardingSource(body.source()) {
                    var received=0L
                    override fun read(sink:Buffer,byteCount:Long):Long {
                        if((ProviderDeadline.current.get() ?: Long.MAX_VALUE)<System.nanoTime()) throw java.net.SocketTimeoutException("Provider deadline exceeded")
                        val n=super.read(sink,byteCount);if(n>0)received+=n
                        if(received>8*1024*1024)throw IOException("Platform response too large")
                        return n
                    }
                }.buffer()
                response.newBuilder().body(object:ResponseBody(){override fun contentType()=body.contentType();override fun contentLength()=body.contentLength();override fun source()=limited}).build()
            }
        }.build()
    fun apiClient() = testClient ?: client
    fun downloadClient() = client
}
