// --- CONFIGURATION ---
// Change this to your Render URL when deploying (e.g., 'your-app.onrender.com')
const BACKEND_DOMAIN = 'localhost:8080'; 

// Set to true when deploying to Render (enables https:// and wss://)
const IS_PRODUCTION = false; 

const API_URL = `${IS_PRODUCTION ? 'https' : 'http'}://${BACKEND_DOMAIN}/api/create`;
const WS_URL = `${IS_PRODUCTION ? 'wss' : 'ws'}://${BACKEND_DOMAIN}/ws/`;
// ---------------------

// DOM Elements
const homeView = document.getElementById('home-view');
const chatView = document.getElementById('chat-view');
const btnCreate = document.getElementById('btn-create');
const btnJoin = document.getElementById('btn-join');
const inputJoin = document.getElementById('input-join');
const homeError = document.getElementById('home-error');

const roomIdDisplay = document.getElementById('room-id-display');
const messagesContainer = document.getElementById('messages-container');
const chatForm = document.getElementById('chat-form');
const messageInput = document.getElementById('message-input');
const btnSend = document.getElementById('btn-send');
const charCurrent = document.getElementById('char-current');
const btnLeave = document.getElementById('btn-leave');
const statusDot = document.querySelector('.dot');
const statusText = document.getElementById('status-text');

let ws = null;
let currentChatId = null;

// Pure UUID generation
function generateUUID() {
    if (crypto.randomUUID) return crypto.randomUUID();
    return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, function(c) {
        var r = Math.random() * 16 | 0, v = c == 'x' ? r : (r & 0x3 | 0x8);
        return v.toString(16);
    });
}

// Retrieve or create temporary UserID
let userId = localStorage.getItem('cheetah_uuid');
if (!userId) {
    userId = generateUUID();
    localStorage.setItem('cheetah_uuid', userId);
}

// Hash Routing (Perfect for GitHub Pages)
// GitHub Pages does not support native SPA URL paths, but fully supports Hash routing (#1234abcd)
window.addEventListener('DOMContentLoaded', () => {
    const hash = window.location.hash.substring(1); // removes the '#'
    if (hash && hash.length > 0) {
        joinChat(hash);
    }
});

window.addEventListener('hashchange', () => {
    const hash = window.location.hash.substring(1);
    if (!hash) {
        leaveChat(); // User clicked back to home
    } else if (hash !== currentChatId) {
        joinChat(hash); // User clicked forward to a chat
    }
});

// Event Listeners
btnCreate.addEventListener('click', async () => {
    try {
        btnCreate.disabled = true;
        btnCreate.innerText = 'Creating...';
        homeError.innerText = '';
        
        const response = await fetch(API_URL, { method: 'POST' });
        
        if (!response.ok) {
            const err = await response.text();
            throw new Error(err);
        }
        
        const data = await response.json();
        joinChat(data.chat_id);
    } catch (error) {
        // If fetch fails completely, it might be a CORS error or server offline
        homeError.innerText = error.message === "Failed to fetch" 
            ? "Cannot connect to server. Is it running?" 
            : error.message;
    } finally {
        btnCreate.disabled = false;
        btnCreate.innerText = 'Create Global Chat';
    }
});

btnJoin.addEventListener('click', () => {
    const id = inputJoin.value.trim();
    if (id) {
        joinChat(id);
    } else {
        homeError.innerText = 'Please enter a Chat ID';
    }
});

inputJoin.addEventListener('keypress', (e) => {
    if (e.key === 'Enter') btnJoin.click();
});

messageInput.addEventListener('input', () => {
    charCurrent.innerText = messageInput.value.length;
});

chatForm.addEventListener('submit', (e) => {
    e.preventDefault();
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    
    const text = messageInput.value.trim();
    if (!text) return;
    
    const payload = `${userId}:${text}`;
    ws.send(payload);
    
    messageInput.value = '';
    charCurrent.innerText = '0';
});

btnLeave.addEventListener('click', () => leaveChat());

// Functions
function joinChat(chatId) {
    currentChatId = chatId;
    
    // Update the URL Hash so it can be shared (e.g. yoursite.github.io/#1234abcd)
    if (window.location.hash !== `#${chatId}`) {
        window.location.hash = chatId;
    }
    
    homeView.classList.remove('active');
    chatView.classList.add('active');
    roomIdDisplay.innerText = chatId;
    messagesContainer.innerHTML = ''; 
    
    connectWebSocket(chatId);
}

function leaveChat(errorMsg = '') {
    if (ws) {
        ws.close();
        ws = null;
    }
    currentChatId = null;
    
    // Clear hash without reloading the page
    if (window.location.hash !== '') {
        window.history.pushState("", document.title, window.location.pathname + window.location.search);
    }
    
    chatView.classList.remove('active');
    homeView.classList.add('active');
    inputJoin.value = '';
    homeError.innerText = errorMsg;
}

function connectWebSocket(chatId) {
    statusDot.classList.remove('active');
    statusText.innerText = "Connecting...";
    
    ws = new WebSocket(`${WS_URL}${chatId}?user=${userId}`);
    
    ws.onopen = () => {
        statusDot.classList.add('active');
        statusText.innerText = "Connected";
        appendMessage('Connected to server', 'msg-system');
        messageInput.disabled = false;
        btnSend.disabled = false;
        messageInput.focus();
    };
    
    ws.onmessage = async (event) => {
        let text = "";
        
        if (event.data instanceof Blob) {
            text = await event.data.text();
        } else {
            text = event.data;
        }
        
        // Handle server rejections
        if (text.startsWith("ERROR:")) {
            if (text.includes("full") || text.includes("not found")) {
                // Instantly boot the user back to the home screen with the error
                leaveChat(text.replace("ERROR: ", ""));
                return;
            } else if (text.includes("locked")) {
                appendMessage(text, 'msg-system');
                messageInput.disabled = true;
                btnSend.disabled = true;
                return;
            }
        }
        
        const sepIndex = text.indexOf(':');
        if (sepIndex === -1) {
            appendMessage(text, 'msg-other');
            return;
        }
        
        const senderId = text.substring(0, sepIndex);
        const actualMessage = text.substring(sepIndex + 1);
        
        const type = (senderId === userId) ? 'msg-self' : 'msg-other';
        appendMessage(actualMessage, type);
    };
    
    ws.onclose = () => {
        // If the websocket closes unexpectedly, we boot them out
        if (currentChatId) {
            leaveChat("Disconnected from server.");
        }
    };
    
    ws.onerror = () => {
        if (currentChatId) {
            leaveChat("Connection error: Chat might not exist.");
        }
    };
}

function appendMessage(text, className) {
    const div = document.createElement('div');
    div.classList.add('message', className);
    div.textContent = text;
    messagesContainer.appendChild(div);
    
    messagesContainer.scrollTo({
        top: messagesContainer.scrollHeight,
        behavior: 'smooth'
    });
}
