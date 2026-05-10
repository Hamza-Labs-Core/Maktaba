package com.maktaba.tv

import android.app.Application
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Text

class MaktabaApp : Application()

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent { Root() }
    }
}

@Composable
fun Root() {
    var isPaired by remember { mutableStateOf(false) }
    if (isPaired) MainScreen() else PairingScreen(onPaired = { isPaired = true })
}

@Composable
fun MainScreen() {
    Column(modifier = Modifier.padding(96.dp)) {
        Text("Maktaba TV — Home")
    }
}

@Composable
fun PairingScreen(onPaired: () -> Unit) {
    Column(modifier = Modifier.padding(96.dp)) {
        Text("Pair this Android TV — code: ABCD-1234")
    }
}
