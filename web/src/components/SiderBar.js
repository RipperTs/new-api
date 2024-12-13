import React, { useContext, useEffect, useMemo, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { UserContext } from '../context/User';
import { StatusContext } from '../context/Status';

import {
  API,
  isAdmin,
  showError,
} from '../helpers';
import '../index.css';

import {
  IconCalendarClock,
  IconHistogram,
  IconKey,
  IconLayers,
  IconSetting,
  IconUser
} from '@douyinfe/semi-icons';
import { Nav, Switch } from '@douyinfe/semi-ui';
import { setStatusData } from '../helpers/data.js';
import { useSetTheme, useTheme } from '../context/Theme/index.js';
import { StyleContext } from '../context/Style/index.js';

// HeaderBar Buttons

const SiderBar = () => {
  const [styleState, styleDispatch] = useContext(StyleContext);
  const [statusState, statusDispatch] = useContext(StatusContext);
  const defaultIsCollapsed =
    localStorage.getItem('default_collapse_sidebar') === 'true';

  const [selectedKeys, setSelectedKeys] = useState(['home']);
  const [isCollapsed, setIsCollapsed] = useState(defaultIsCollapsed);
  const [chatItems, setChatItems] = useState([]);

  const routerMap = {
    home: '/',
    channel: '/channel',
    token: '/token',
    redemption: '/redemption',
    user: '/user',
    log: '/log',
    midjourney: '/midjourney',
    setting: '/setting',
    about: '/about',
    detail: '/detail',
    pricing: '/pricing',
    task: '/task',
  };

  const headerButtons = useMemo(
    () => [
      {
        text: '渠道',
        itemKey: 'channel',
        to: '/channel',
        icon: <IconLayers />,
        className: isAdmin() ? 'semi-navigation-item-normal' : 'tableHiddle',
      },
      {
        text: '令牌',
        itemKey: 'token',
        to: '/token',
        icon: <IconKey />,
      },
      {
        text: '用户管理',
        itemKey: 'user',
        to: '/user',
        icon: <IconUser />,
        className: isAdmin() ? 'semi-navigation-item-normal' : 'tableHiddle',
      },
      {
        text: '日志',
        itemKey: 'log',
        to: '/log',
        icon: <IconHistogram />,
      },
      {
        text: '数据看板',
        itemKey: 'detail',
        to: '/detail',
        icon: <IconCalendarClock />,
        className:
          localStorage.getItem('enable_data_export') === 'true'
            ? 'semi-navigation-item-normal'
            : 'tableHiddle',
      },

      {
        text: '设置',
        itemKey: 'setting',
        to: '/setting',
        icon: <IconSetting />,
      }
    ],
    [
      localStorage.getItem('enable_data_export'),
      localStorage.getItem('enable_drawing'),
      localStorage.getItem('enable_task'),
      localStorage.getItem('chat_link'), chatItems,
      isAdmin(),
    ],
  );

  const loadStatus = async () => {
    const res = await API.get('/api/status');
    if (res === undefined) {
      return;
    }
    const { success, data } = res.data;
    if (success) {
      statusDispatch({ type: 'set', payload: data });
      setStatusData(data);
    } else {
      showError('无法正常连接至服务器！');
    }
  };

  useEffect(() => {
    loadStatus().then(() => {
      setIsCollapsed(
          localStorage.getItem('default_collapse_sidebar') === 'true',
      );
    });
    let localKey = window.location.pathname.split('/')[1];
    if (localKey === '') {
      localKey = 'home';
    }
    setSelectedKeys([localKey]);
    let chatLink = localStorage.getItem('chat_link');
    if (!chatLink) {
        let chats = localStorage.getItem('chats');
        if (chats) {
            // console.log(chats);
            try {
                chats = JSON.parse(chats);
                if (Array.isArray(chats)) {
                    let chatItems = [];
                    for (let i = 0; i < chats.length; i++) {
                        let chat = {};
                        for (let key in chats[i]) {
                            chat.text = key;
                            chat.itemKey = 'chat' + i;
                            chat.to = '/chat/' + i;
                        }
                        // setRouterMap({ ...routerMap, chat: '/chat/' + i })
                        chatItems.push(chat);
                    }
                    setChatItems(chatItems);
                }
            } catch (e) {
                console.error(e);
                showError('聊天数据解析失败')
            }
        }
    }
  }, []);

  return (
    <>
      <Nav
        style={{ maxWidth: 220, height: '100%' }}
        defaultIsCollapsed={
          localStorage.getItem('default_collapse_sidebar') === 'true'
        }
        isCollapsed={isCollapsed}
        onCollapseChange={(collapsed) => {
          setIsCollapsed(collapsed);
        }}
        selectedKeys={selectedKeys}
        renderWrapper={({ itemElement, isSubNav, isInSubNav, props }) => {
            let chatLink = localStorage.getItem('chat_link');
            if (!chatLink) {
                let chats = localStorage.getItem('chats');
                if (chats) {
                    chats = JSON.parse(chats);
                    if (Array.isArray(chats) && chats.length > 0) {
                        for (let i = 0; i < chats.length; i++) {
                            routerMap['chat' + i] = '/chat/' + i;
                        }
                        if (chats.length > 1) {
                            // delete /chat
                            if (routerMap['chat']) {
                                delete routerMap['chat'];
                            }
                        } else {
                            // rename /chat to /chat/0
                            routerMap['chat'] = '/chat/0';
                        }
                    }
                }
            }
          return (
            <Link
              style={{ textDecoration: 'none' }}
              to={routerMap[props.itemKey]}
            >
              {itemElement}
            </Link>
          );
        }}
        items={headerButtons}
        onSelect={(key) => {
          if (key.itemKey.toString().startsWith('chat')) {
            styleDispatch({ type: 'SET_INNER_PADDING', payload: false });
          } else {
            styleDispatch({ type: 'SET_INNER_PADDING', payload: true });
          }
          setSelectedKeys([key.itemKey]);
        }}
        footer={
          <>
          </>
        }
      >
        <Nav.Footer collapseButton={true}></Nav.Footer>
      </Nav>
    </>
  );
};

export default SiderBar;
