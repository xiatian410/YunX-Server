package com.yunx.server

import com.yunx.app.data.network.HttpClients
import kotlinx.coroutines.runBlocking
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.json.JSONObject
import org.junit.After
import org.junit.Test
import kotlin.test.*
import java.net.InetAddress

class AdapterContractTest {
    @After fun reset() { HttpClients.testClient=null }
    private fun fixture(reply: (Request)->String) {
        HttpClients.testClient=OkHttpClient.Builder().addInterceptor { chain ->
            Response.Builder().request(chain.request()).protocol(Protocol.HTTP_1_1).code(200).message("Fixture")
                .body(reply(chain.request()).toResponseBody("application/json".toMediaType())).build()
        }.build()
    }
    @Test fun quarkCloudDoesNotStopAtServerPageCap()= runBlocking {
        fixture { request ->
            assertEquals("session=fixture",request.header("Cookie"));assertEquals("2",request.url.queryParameter("_page"))
            """{"status":200,"data":{"list":[{"fid":"9007199254740993","file_name":"fixture.bin","size":42,"dir":false}]}}"""
        }
        val result=Adapter(JSONObject("""{"op":"list","platform":"quark","cloud":true,"parent":"0","page":2,"credentials":{"cookie":"session=fixture"}}""")).execute()
        assertTrue(result.getBoolean("more"));assertEquals("9007199254740993",result.getJSONArray("items").getJSONObject(0).getString("id"))
    }
    @Test fun xunleiCloudPassesCursor()=runBlocking {
        fixture { request ->
            assertEquals("next-page",request.url.queryParameter("page_token"))
            """{"files":[{"id":"f1","name":"fixture.bin","kind":"drive#file","size":"42"}],"next_page_token":"last-page"}"""
        }
        val result=Adapter(JSONObject("""{"op":"list","platform":"xunlei","cloud":true,"parent":"0","cursor":"next-page","credentials":{"token":"fixture","device_id":"device","captcha_token":"captcha"}}""")).execute()
        assertEquals("last-page",result.getString("cursor"));assertTrue(result.getBoolean("more"));assertEquals("f1",result.getJSONArray("items").getJSONObject(0).getString("id"))
    }
    @Test fun unsupportedAccountCheckIsNotClaimedValid()=runBlocking {
        for(platform in listOf("douyin","kuaishou","weibo","acfun")) {
            val result=Adapter(JSONObject().put("op","check").put("platform",platform).put("credentials",JSONObject().put("cookie","not-a-valid-session"))).execute()
            assertFalse(result.getBoolean("known"));assertFalse(result.getBoolean("valid"))
        }
    }
    @Test fun bilibiliPartsSelectCorrectCID()=runBlocking {
        var cid=""
        fixture { request ->
            if(request.url.encodedPath.endsWith("/view")) """{"code":0,"data":{"bvid":"BV1xx411c7mD","title":"Course","pages":[{"cid":101,"part":"First"},{"cid":202,"part":"Second"}]}}"""
            else { cid=request.url.queryParameter("cid").orEmpty();"""{"code":0,"data":{"quality":80,"accept_quality":[80],"accept_description":["1080P"],"durl":[{"url":"https://cdn.example/video.mp4","size":42}]}}""" }
        }
        val request=JSONObject("""{"op":"resolve","platform":"bilibili","text":"https://www.bilibili.com/video/BV1xx411c7mD"}""")
        val result=Adapter(request).execute();assertEquals(2,result.getJSONArray("items").length())
        request.put("op","sources").put("item",result.getJSONArray("items").getJSONObject(1))
        val sources=Adapter(request).execute().getJSONArray("sources")
        assertEquals("202",cid);assertEquals("https://cdn.example/video.mp4",sources.getJSONObject(0).getString("url"))
    }
    @Test fun malformedCloudListsAreNotEmptyFolders()=runBlocking {
        fixture { """{"status":200,"code":0,"state":true,"data":{}}""" }
        for(platform in listOf("quark","uc","pan123","pan115")) {
            val request=JSONObject().put("op","list").put("platform",platform).put("cloud",true).put("credentials",JSONObject().put("cookie","fixture").put("token","fixture"))
            assertFailsWith<Exception>(platform) { Adapter(request).execute() }
        }
    }
    @Test fun pan123CloudPassesPageAndCursor()=runBlocking {
        fixture { request ->
            assertEquals("cursor-1",request.url.queryParameter("next"));assertEquals("2",request.url.queryParameter("Page"))
            """{"code":0,"data":{"InfoList":[],"Next":"-1"}}"""
        }
        val result=Adapter(JSONObject("""{"op":"list","platform":"pan123","cloud":true,"parent":"0","page":2,"cursor":"cursor-1","credentials":{"token":"fixture"}}""")).execute()
        assertFalse(result.getBoolean("more"));assertEquals(0,result.getJSONArray("items").length())
    }
    @Test fun publicNetworkBoundary() {
        for(ip in listOf("127.0.0.1","10.1.2.3","192.168.1.1","169.254.169.254","100.64.0.1","::1","fd00::1"))assertFalse(HttpClients.publicAddress(InetAddress.getByName(ip)),ip)
        assertTrue(HttpClients.publicAddress(InetAddress.getByName("8.8.8.8")))
    }
}
